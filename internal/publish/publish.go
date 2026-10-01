// Package publish holds Radaro's own platform connectors: publish a post or a
// reply to a connected account and read back its engagement metrics.
//
// Connectors use public APIs, or an isolated Chromium session for Reddit
// accounts connected with a login and password.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/netproxy"
)

// Post is what a draft asks a connector to publish.
type Post struct {
	Kind      string // post | reply
	Community string // subreddit for Reddit; comma-separated tags for Dev.to
	Title     string // Reddit and Dev.to only
	Body      string
	ReplyTo   string // URL (or platform ID) of the post/comment to answer
	// IdempotencyKey lets platforms that support it drop a duplicate submit.
	IdempotencyKey string
}

// Result identifies what was published.
type Result struct {
	RemoteID string `json:"remote_id"`
	URL      string `json:"url"`
}

// Metrics is a platform's engagement snapshot, e.g. {"likes": 3, "replies": 1}.
type Metrics map[string]any

// Publisher is one connected account on one platform.
type Publisher interface {
	Publish(ctx context.Context, p Post) (Result, error)
	Metrics(ctx context.Context, remoteID string) (Metrics, error)
}

// Platform describes a supported publishing platform.
type Platform struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Replies  bool   `json:"replies"`   // can answer an existing thread
	Titles   bool   `json:"titles"`    // posts carry a title
	MaxChars int    `json:"max_chars"` // 0 = no practical limit
}

// Platforms lists the supported platforms.
var Platforms = []Platform{
	{Name: "reddit", Label: "Reddit", Replies: true, Titles: true, MaxChars: 40000},
	{Name: "bluesky", Label: "Bluesky", Replies: true, MaxChars: 300},
	{Name: "mastodon", Label: "Mastodon", Replies: true, MaxChars: 500},
	{Name: "devto", Label: "Dev.to", Titles: true},
}

// LookupPlatform returns the platform called name.
func LookupPlatform(name string) (Platform, bool) {
	for _, p := range Platforms {
		if p.Name == name {
			return p, true
		}
	}
	return Platform{}, false
}

// Validate checks a post against the platform's shape before anything is sent.
func (pl Platform) Validate(p Post) error {
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return errors.New("body must not be empty")
	}
	if p.Kind == "reply" && !pl.Replies {
		return fmt.Errorf("%s does not support replies", pl.Label)
	}
	if p.Kind == "post" && pl.Titles && strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("%s posts need a title", pl.Label)
	}
	if pl.Name == "reddit" && p.Kind == "post" && strings.TrimSpace(p.Community) == "" {
		return errors.New("reddit posts need a subreddit (community)")
	}
	if pl.Name == "reddit" && len([]rune(p.Title)) > 300 {
		return errors.New("reddit titles allow 300 characters")
	}
	if pl.MaxChars > 0 && len([]rune(body)) > pl.MaxChars {
		return fmt.Errorf("%s allows %d characters, the body has %d", pl.Label, pl.MaxChars, len([]rune(body)))
	}
	return nil
}

// New builds the publisher for a stored account.
func New(platform string, credentials json.RawMessage, userAgentVersion string) (Publisher, error) {
	switch platform {
	case "bluesky":
		var c BlueskyCredentials
		if err := json.Unmarshal(credentials, &c); err != nil {
			return nil, err
		}
		return &Bluesky{Creds: c}, nil
	case "mastodon":
		var c MastodonCredentials
		if err := json.Unmarshal(credentials, &c); err != nil {
			return nil, err
		}
		return &Mastodon{Creds: c}, nil
	case "devto":
		var c DevtoCredentials
		if err := json.Unmarshal(credentials, &c); err != nil {
			return nil, err
		}
		return &Devto{Creds: c}, nil
	case "reddit":
		var c RedditCredentials
		if err := json.Unmarshal(credentials, &c); err != nil {
			return nil, err
		}
		return &Reddit{Creds: c, Version: userAgentVersion}, nil
	}
	return nil, fmt.Errorf("unknown platform %q", platform)
}

// APIError is a non-2xx platform response that is neither a rate limit
// (RateLimitError) nor a refusal of the account (AccountError).
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

var httpClient = &http.Client{Timeout: 30 * time.Second, Transport: netproxy.Transport()}

type request struct {
	method      string
	url         string
	headers     map[string]string
	body        io.Reader
	contentType string
}

// do sends req and decodes a JSON response into out (which may be nil).
func do(ctx context.Context, req request, out any) error {
	r, err := http.NewRequestWithContext(ctx, req.method, req.url, req.body)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "application/json")
	if req.contentType != "" {
		r.Header.Set("Content-Type", req.contentType)
	}
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", "radaro (+https://github.com/verrloren/radaro)")
	}
	for k, v := range req.headers {
		r.Header.Set(k, v)
	}
	resp, err := httpClient.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp.StatusCode, resp.Header, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected response: %s", snippet(raw))
	}
	return nil
}

func jsonBody(v any) (io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(string(b)), nil
}

func snippet(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}
