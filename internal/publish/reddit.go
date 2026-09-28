package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	redditWWW   = "https://www.reddit.com"
	redditOAuth = "https://oauth.reddit.com"
)

// RedditScopes are what Radaro asks for: who you are, submit posts/comments,
// and read them back for metrics.
const RedditScopes = "identity submit read"

// RedditCredentials come from `radaro connect reddit`: the user's own Reddit
// app (reddit.com/prefs/apps) plus the permanent refresh token it granted.
type RedditCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURI  string `json:"redirect_uri"`
	RefreshToken string `json:"refresh_token"`
	Username     string `json:"username"`
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
	var out map[string]any
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Status: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
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
		return "", errors.New("reddit account is not connected; run radaro connect reddit")
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
		Errors [][]any `json:"errors"`
		Data   struct {
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
	for _, item := range e.JSON.Errors {
		var s []string
		for _, v := range item {
			if str, ok := v.(string); ok && str != "" {
				s = append(s, str)
			}
		}
		parts = append(parts, strings.Join(s, ": "))
	}
	return errors.New("reddit rejected it: " + strings.Join(parts, "; "))
}

func (r *Reddit) Publish(ctx context.Context, p Post) (Result, error) {
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
	sr := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(p.Community), "/"), "r/"), "/")
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

func (r *Reddit) Metrics(ctx context.Context, remoteID string) (Metrics, error) {
	var out struct {
		Data struct {
			Children []struct {
				Data struct {
					Score       int64    `json:"score"`
					NumComments *int64   `json:"num_comments"`
					UpvoteRatio *float64 `json:"upvote_ratio"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := r.api(ctx, "GET", "/api/info?id="+url.QueryEscape(remoteID), nil, &out); err != nil {
		return nil, err
	}
	if len(out.Data.Children) == 0 {
		return nil, errors.New("not found (removed?)")
	}
	d := out.Data.Children[0].Data
	m := Metrics{"score": d.Score}
	if d.NumComments != nil {
		m["comments"] = *d.NumComments
	}
	if d.UpvoteRatio != nil {
		m["upvote_ratio"] = *d.UpvoteRatio
	}
	return m, nil
}
