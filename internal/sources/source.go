// Package sources holds the pluggable mention adapters.
//
// Hacker News, Bluesky and Stack Overflow need no credentials; the others are
// enabled with operator-supplied configuration.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// UserAgent identifies Radaro to source APIs.
const UserAgent = "radaro/0.1 (+https://github.com/verrloren/radaro)"

// Page is one source page plus an opaque cursor for the next, older page.
type Page struct {
	Mentions   []model.Mention
	NextCursor string // empty when there is no older page
}

// Source fetches mentions for a query. Implementations should return an error
// rather than panic; the pipeline isolates per-source failures.
type Source interface {
	// FetchPage returns one page. cursor continues an earlier page; since is a
	// best-effort lower bound for incremental scans (zero = none).
	FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error)
}

// Options carries the credentials and settings keyed sources need.
type Options struct {
	RedditClientID      string
	RedditClientSecret  string
	RedditAccessToken   string
	RedditRefreshToken  string
	MastodonInstance    string
	MastodonAccessToken string
	RSSFeeds            []string
	XBearerToken        string
	YouTubeAPIKey       string
}

// Info describes a registered source.
type Info struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Glyph       string `json:"glyph"`
	Color       string `json:"color"`
	NeedsConfig bool   `json:"needs_config"`
}

type entry struct {
	info       Info
	configured func(Options) bool
	build      func(Options) Source
}

var registry = []entry{
	{Info{"hackernews", "Hacker News", "Y", "#ff6a3d", false}, always, func(Options) Source { return &HackerNews{} }},
	{Info{"reddit", "Reddit", "r/", "#ff4f3f", true},
		func(o Options) bool {
			return o.RedditAccessToken != "" || (o.RedditClientID != "" && (o.RedditClientSecret != "" || o.RedditRefreshToken != ""))
		},
		func(o Options) Source {
			return &Reddit{ClientID: o.RedditClientID, ClientSecret: o.RedditClientSecret, RefreshToken: o.RedditRefreshToken, AccessToken: o.RedditAccessToken}
		}},
	{Info{"mastodon", "Mastodon", "@", "#7c7fff", true},
		func(o Options) bool { return o.MastodonAccessToken != "" },
		func(o Options) Source { return NewMastodon(o.MastodonInstance, o.MastodonAccessToken) }},
	{Info{"bluesky", "Bluesky", "◈", "#3aa8ff", false}, always, func(Options) Source { return &Bluesky{} }},
	{Info{"rss", "RSS", "∿", "#e0a23a", true},
		func(o Options) bool { return len(o.RSSFeeds) > 0 },
		func(o Options) Source { return &RSS{Feeds: o.RSSFeeds} }},
	{Info{"stackoverflow", "Stack Overflow", "<>", "#f48024", false}, always, func(Options) Source { return &StackOverflow{} }},
	{Info{"x", "X / Twitter", "X", "#d8dce5", true},
		func(o Options) bool { return o.XBearerToken != "" },
		func(o Options) Source { return &X{BearerToken: o.XBearerToken} }},
	{Info{"youtube", "YouTube", "▶", "#ff3d3d", true},
		func(o Options) bool { return o.YouTubeAPIKey != "" },
		func(o Options) Source { return &YouTube{APIKey: o.YouTubeAPIKey} }},
}

func always(Options) bool { return true }

// DefaultSources work with zero configuration (no key, no account).
var DefaultSources = []string{"hackernews", "bluesky"}

// All lists every registered source in display order.
func All() []Info {
	out := make([]Info, len(registry))
	for i, e := range registry {
		out[i] = e.info
	}
	return out
}

// Lookup returns the metadata for name.
func Lookup(name string) (Info, bool) {
	for _, e := range registry {
		if e.info.Name == name {
			return e.info, true
		}
	}
	return Info{}, false
}

// Configured reports whether name has the credentials it needs.
func Configured(name string, o Options) bool {
	for _, e := range registry {
		if e.info.Name == name {
			return e.configured(o)
		}
	}
	return false
}

// New builds the source called name.
func New(name string, o Options) (Source, error) {
	for _, e := range registry {
		if e.info.Name == name {
			return e.build(o), nil
		}
	}
	return nil, fmt.Errorf("unknown source")
}

// HTTPError is a non-2xx response. The pipeline retries 429 and 5xx.
type HTTPError struct {
	Status     int
	RetryAfter string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d", e.Status) }

// Retryable reports whether err is worth retrying: network errors, HTTP 429 and 5xx.
func Retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	var ue *url.Error
	return errors.As(err, &ue)
}

var client = &http.Client{Timeout: 15 * time.Second}

// getJSON performs a GET with Radaro's user agent and decodes a JSON body into out.
func getJSON(ctx context.Context, endpoint string, params url.Values, headers map[string]string, out any) error {
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return doJSON(req, headers, out)
}

func doJSON(req *http.Request, headers map[string]string, out any) error {
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

var (
	tagRE   = regexp.MustCompile(`<[^>]+>`)
	spaceRE = regexp.MustCompile(`\s+`)
)

// StripHTML turns the small HTML fragments returned by feeds into readable text.
func StripHTML(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRE.ReplaceAllString(html.UnescapeString(s), " "))
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func unixTime(ts int64) time.Time {
	if ts == 0 {
		return time.Now().UTC()
	}
	return time.Unix(ts, 0).UTC()
}

func rfc3339(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
