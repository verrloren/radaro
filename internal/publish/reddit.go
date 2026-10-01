package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/redditbrowser"
)

var (
	redditWWW   = "https://www.reddit.com"
	redditOAuth = "https://oauth.reddit.com"
)

// RedditScopes are what Radaro asks for: who you are, submit posts/comments,
// and read them back for metrics.
const RedditScopes = "identity submit read"

// RedditCredentials hold either OAuth app credentials or a browser session.
type RedditCredentials struct {
	ClientID     string                     `json:"client_id"`
	ClientSecret string                     `json:"client_secret"`
	RedirectURI  string                     `json:"redirect_uri"`
	RefreshToken string                     `json:"refresh_token"`
	Username     string                     `json:"username"`
	Browser      *redditbrowser.Credentials `json:"browser,omitempty"`
}

// Reddit submits self posts and comments.
type Reddit struct {
	Creds   RedditCredentials
	Version string
	token   string
}

// RedditAuthorizeURL is where the user grants access during connect.
func RedditAuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id": {clientID}, "response_type": {"code"}, "state": {state},
		"redirect_uri": {redirectURI}, "duration": {"permanent"}, "scope": {RedditScopes},
	}
	return redditWWW + "/api/v1/authorize?" + q.Encode()
}

func (r *Reddit) userAgent() string {
	v := r.Version
	if v == "" {
		v = "dev"
	}
	// Reddit asks for <platform>:<app id>:<version> (by /u/<username>).
	ua := "cli:radaro:" + v
	if r.Creds.Username != "" {
		ua += " (by /u/" + r.Creds.Username + ")"
	}
	return ua
}

func (r *Reddit) tokenRequest(ctx context.Context, form url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", redditWWW+"/api/v1/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(r.Creds.ClientID, r.Creds.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", r.userAgent())
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	decodeErr := json.Unmarshal(raw, &out)
	// A revoked or expired grant comes back as invalid_grant, with HTTP 400
	// or 200 depending on the grant type.
	if msg, _ := out["error"].(string); msg == "invalid_grant" {
		return nil, &AccountError{Status: HealthInvalid, Detail: "reddit refused the grant (invalid_grant); reconnect the account"}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &AccountError{Status: HealthInvalid, Detail: "reddit rejected the app's client id or secret"}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp.StatusCode, resp.Header, raw)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("unexpected response: %s", snippet(raw))
	}
	if msg, ok := out["error"].(string); ok {
		return nil, fmt.Errorf("reddit OAuth error: %s", msg)
	}
	return out, nil
}

// ExchangeCode finishes `radaro connect reddit`: it trades the authorization
// code for a permanent refresh token and looks up the username.
func (r *Reddit) ExchangeCode(ctx context.Context, code string) error {
	out, err := r.tokenRequest(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {r.Creds.RedirectURI}})
	if err != nil {
		return err
	}
	refresh, _ := out["refresh_token"].(string)
	access, _ := out["access_token"].(string)
	if refresh == "" || access == "" {
		return errors.New("reddit did not return a refresh token (was duration=permanent granted?)")
	}
	r.Creds.RefreshToken, r.token = refresh, access
	var me struct {
		Name string `json:"name"`
	}
	if err := r.api(ctx, "GET", "/api/v1/me", nil, &me); err != nil {
		return err
	}
	r.Creds.Username = me.Name
	return nil
}

func (r *Reddit) accessToken(ctx context.Context) (string, error) {
	if r.token != "" {
		return r.token, nil
	}
	if r.Creds.RefreshToken == "" {
		return "", &AccountError{Status: HealthInvalid, Detail: "reddit account is not connected; run radaro connect reddit"}
	}
	out, err := r.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {r.Creds.RefreshToken}})
	if err != nil {
		return "", fmt.Errorf("reddit token refresh failed: %w", err)
	}
	r.token, _ = out["access_token"].(string)
	if r.token == "" {
		return "", errors.New("reddit token refresh returned no access token")
	}
	return r.token, nil
}

func (r *Reddit) api(ctx context.Context, method, path string, form url.Values, out any) error {
	cached := r.token != ""
	err := r.call(ctx, method, path, form, out)
	var ae *AccountError
	if cached && errors.As(err, &ae) && ae.Status == HealthInvalid {
		// Access tokens live an hour: a 401 on a cached one asks for a new
		// token, it does not mean the account is dead. Nothing was done, so
		// the retry cannot duplicate a post.
		r.token = ""
		err = r.call(ctx, method, path, form, out)
	}
	return err
}

func (r *Reddit) call(ctx context.Context, method, path string, form url.Values, out any) error {
	token, err := r.accessToken(ctx)
	if err != nil {
		return err
	}
	req := request{method: method, url: redditOAuth + path,
		headers: map[string]string{"Authorization": "Bearer " + token, "User-Agent": r.userAgent()}}
	if form != nil {
		req.body, req.contentType = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	}
	return do(ctx, req, out)
}

// redditJSON is the api_type=json envelope. Reddit reports refusals (rate
// limits, rule violations) as HTTP 200 with a non-empty errors array.
type redditJSON struct {
	JSON struct {
		Errors    [][]any `json:"errors"`
		Ratelimit float64 `json:"ratelimit"` // seconds, sent with some RATELIMIT errors
		Data      struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			Things []struct {
				Data struct {
					Name      string `json:"name"`
					Permalink string `json:"permalink"`
				} `json:"data"`
			} `json:"things"`
		} `json:"data"`
	} `json:"json"`
}

func (e redditJSON) err() error {
	if len(e.JSON.Errors) == 0 {
		return nil
	}
	var parts []string
	limited := false
	for _, item := range e.JSON.Errors {
		var s []string
		for _, v := range item {
			if str, ok := v.(string); ok && str != "" {
				s = append(s, str)
			}
		}
		if len(s) > 0 && s[0] == "RATELIMIT" {
			limited = true
		}
		parts = append(parts, strings.Join(s, ": "))
	}
	msg := strings.Join(parts, "; ")
	if limited {
		wait := seconds(e.JSON.Ratelimit)
		if wait <= 0 {
			wait = redditWait(msg)
		}
		return &RateLimitError{RetryAfter: wait, Detail: "reddit: " + msg}
	}
	return errors.New("reddit rejected it: " + msg)
}

var redditTryAgain = regexp.MustCompile(`(?i)try again in (\d+) (millisecond|second|minute|hour)s?`)

// redditWait parses "you are doing that too much. try again in 9 minutes."
func redditWait(msg string) time.Duration {
	m := redditTryAgain.FindStringSubmatch(msg)
	if m == nil {
		return defaultRetryAfter
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"millisecond": time.Millisecond, "second": time.Second, "minute": time.Minute, "hour": time.Hour}[strings.ToLower(m[2])]
	if d := time.Duration(n) * unit; d > 0 {
		return d
	}
	return defaultRetryAfter
}

func (r *Reddit) Publish(ctx context.Context, p Post) (Result, error) {
	if r.Creds.Browser != nil {
		res, err := redditbrowser.Publish(ctx, *r.Creds.Browser, redditbrowser.Post{Kind: p.Kind, Community: p.Community, Title: p.Title, Body: p.Body, ReplyTo: p.ReplyTo})
		return Result{RemoteID: res.RemoteID, URL: res.URL}, browserError(err)
	}
	var out redditJSON
	if p.Kind == "reply" {
		thing, err := RedditThingID(p.ReplyTo)
		if err != nil {
			return Result{}, err
		}
		if err := r.api(ctx, "POST", "/api/comment", url.Values{"api_type": {"json"}, "thing_id": {thing}, "text": {p.Body}}, &out); err != nil {
			return Result{}, err
		}
		if err := out.err(); err != nil {
			return Result{}, err
		}
		if len(out.JSON.Data.Things) == 0 {
			return Result{}, errors.New("reddit accepted the comment but returned no id")
		}
		c := out.JSON.Data.Things[0].Data
		return Result{RemoteID: c.Name, URL: redditWWW + c.Permalink}, nil
	}
	sr := subredditName(p.Community)
	form := url.Values{"api_type": {"json"}, "kind": {"self"}, "sr": {sr}, "title": {p.Title}, "text": {p.Body}}
	if err := r.api(ctx, "POST", "/api/submit", form, &out); err != nil {
		return Result{}, err
	}
	if err := out.err(); err != nil {
		return Result{}, err
	}
	return Result{RemoteID: out.JSON.Data.Name, URL: out.JSON.Data.URL}, nil
}

var redditCommentsURL = regexp.MustCompile(`/comments/([a-z0-9]+)(?:/[^/]*/([a-z0-9]+))?`)

// RedditThingID accepts a post or comment link, or a t3_/t1_ fullname.
func RedditThingID(target string) (string, error) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "t3_") || strings.HasPrefix(target, "t1_") {
		return target, nil
	}
	m := redditCommentsURL.FindStringSubmatch(target)
	if m == nil {
		return "", fmt.Errorf("not a Reddit post or comment link: %s", target)
	}
	if m[2] != "" {
		return "t1_" + m[2], nil
	}
	return "t3_" + m[1], nil
}

// subredditName accepts "golang", "r/golang" or "/r/golang/".
func subredditName(s string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "/"), "r/"), "/")
}

func (r *Reddit) Metrics(ctx context.Context, remoteID string) (Metrics, error) {
	if r.Creds.Browser != nil {
		m, err := redditbrowser.Metrics(ctx, *r.Creds.Browser, remoteID)
		return Metrics(m), browserError(err)
	}
	var out struct {
		Data struct {
			Children []struct {
				Data struct {
					Score             int64    `json:"score"`
					NumComments       *int64   `json:"num_comments"`
					UpvoteRatio       *float64 `json:"upvote_ratio"`
					Body              string   `json:"body"`     // comments
					Selftext          string   `json:"selftext"` // posts
					RemovedByCategory *string  `json:"removed_by_category"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := r.api(ctx, "GET", "/api/info?id="+url.QueryEscape(remoteID), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data.Children) == 0 {
		return Metrics{MetricRemoved: true}, nil
	}
	d := out.Data.Children[0].Data
	m := Metrics{"score": d.Score}
	if d.NumComments != nil {
		m["comments"] = *d.NumComments
	}
	if d.UpvoteRatio != nil {
		m["upvote_ratio"] = *d.UpvoteRatio
	}
	gone := func(text string) bool { return text == "[removed]" || text == "[deleted]" }
	if gone(d.Body) || gone(d.Selftext) || (d.RemovedByCategory != nil && *d.RemovedByCategory != "") {
		m[MetricRemoved] = true
	}
	return m, nil
}

// Check verifies the refresh token and asks Reddit whether the account is suspended.
func (r *Reddit) Check(ctx context.Context) (Health, error) {
	if r.Creds.Browser != nil {
		err := redditbrowser.WithAccount(ctx, *r.Creds.Browser, func(*redditbrowser.Session) error { return nil })
		if err != nil {
			return healthOf(browserError(err))
		}
		return Health{Status: HealthLive, Detail: "u/" + r.Creds.Username + " (browser)"}, nil
	}
	var me struct {
		Name        string `json:"name"`
		IsSuspended bool   `json:"is_suspended"`
	}
	if err := r.api(ctx, "GET", "/api/v1/me", nil, &me); err != nil {
		return healthOf(err)
	}
	if me.IsSuspended {
		return Health{Status: HealthSuspended, Detail: "u/" + me.Name + " is suspended"}, nil
	}
	return Health{Status: HealthLive, Detail: "u/" + me.Name}, nil
}

// SubredditRules returns a subreddit's posted rules.
func (r *Reddit) SubredditRules(ctx context.Context, subreddit string) ([]SubredditRule, error) {
	if r.Creds.Browser != nil {
		rules, err := redditbrowser.Rules(ctx, *r.Creds.Browser, subredditName(subreddit))
		out := make([]SubredditRule, 0, len(rules))
		for _, rule := range rules {
			out = append(out, SubredditRule{Name: rule.Name, Description: rule.Description})
		}
		return out, browserError(err)
	}
	sr := subredditName(subreddit)
	if sr == "" {
		return nil, errors.New("subreddit is required")
	}
	var out struct {
		Rules []struct {
			ShortName   string `json:"short_name"`
			Description string `json:"description"`
		} `json:"rules"`
	}
	if err := r.api(ctx, "GET", "/r/"+url.PathEscape(sr)+"/about/rules", nil, &out); err != nil {
		return nil, err
	}
	rules := make([]SubredditRule, 0, len(out.Rules))
	for _, rule := range out.Rules {
		rules = append(rules, SubredditRule{Name: rule.ShortName, Description: rule.Description})
	}
	return rules, nil
}

func browserError(err error) error {
	if errors.Is(err, redditbrowser.ErrSession) {
		return &AccountError{Status: HealthInvalid, Detail: err.Error()}
	}
	return err
}
