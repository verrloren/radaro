package publish

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func pinClock(t *testing.T, at time.Time) {
	t.Helper()
	old := clock
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = old })
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	pinClock(t, now)
	cases := []struct {
		name   string
		header map[string]string
		want   time.Duration
	}{
		{"seconds", map[string]string{"Retry-After": "120"}, 2 * time.Minute},
		{"http date", map[string]string{"Retry-After": now.Add(90 * time.Second).Format(http.TimeFormat)}, 90 * time.Second},
		{"bluesky unix reset", map[string]string{"ratelimit-reset": "1790769900"}, 5 * time.Minute},
		{"mastodon iso reset", map[string]string{"X-RateLimit-Reset": "2026-09-30T12:10:00.000Z"}, 10 * time.Minute},
		{"reddit seconds left", map[string]string{"x-ratelimit-reset": "42"}, 42 * time.Second},
		{"retry-after wins", map[string]string{"Retry-After": "7", "X-RateLimit-Reset": "2026-09-30T13:00:00Z"}, 7 * time.Second},
		{"past date falls through", map[string]string{"Retry-After": now.Add(-time.Hour).Format(http.TimeFormat), "ratelimit-reset": "1790769630"}, 30 * time.Second},
		{"garbage", map[string]string{"Retry-After": "soon", "X-RateLimit-Reset": "later"}, defaultRetryAfter},
		{"absent", nil, defaultRetryAfter},
	}
	for _, c := range cases {
		h := http.Header{}
		for k, v := range c.header {
			h.Set(k, v)
		}
		if got := retryAfter(h); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestResponseErrorClassifies(t *testing.T) {
	h := http.Header{"Retry-After": {"30"}}
	cases := []struct {
		status int
		body   string
		want   string // Classify status, "" for APIError
	}{
		{429, `{"error":"RateLimitExceeded","message":"Rate Limit Exceeded"}`, HealthLimited},
		{401, `{"error":"AuthenticationRequired","message":"Invalid identifier or password"}`, HealthInvalid},
		{401, `{"error":"The access token is invalid"}`, HealthInvalid},
		{401, ``, HealthInvalid},
		{401, `{"error":"AccountTakedown","message":"Account has been taken down"}`, HealthSuspended},
		{400, `{"error":"AccountDeactivated","message":"Account is deactivated"}`, HealthSuspended},
		{403, `{"error":"Your login is currently disabled"}`, HealthSuspended},
		{403, `{"error":"Your account is suspended"}`, HealthSuspended},
		{403, `{"message":"Forbidden","error":403}`, ""},
		{400, `{"error":"InvalidRequest","message":"Record/text must not be longer than 300 graphemes"}`, ""},
		{404, `not found`, ""},
		{500, `<html>oops</html>`, ""},
	}
	for _, c := range cases {
		err := responseError(c.status, h, []byte(c.body))
		status, wait := Classify(err)
		if status != c.want {
			t.Errorf("%d %s: classified %q (%v), want %q", c.status, c.body, status, err, c.want)
		}
		if c.want == HealthLimited && wait != 30*time.Second {
			t.Errorf("429 wait %s", wait)
		}
		var api *APIError
		if c.want == "" && (!errors.As(err, &api) || api.Status != c.status) {
			t.Errorf("%d: want an APIError, got %T %v", c.status, err, err)
		}
	}
	err := responseError(403, nil, []byte(`{"error":"Your login is currently disabled"}`))
	if err.Error() != "suspended: Your login is currently disabled" {
		t.Errorf("message %q", err.Error())
	}
}

// fakeReddit points the Reddit endpoints at two local servers.
func fakeReddit(t *testing.T, token, api http.HandlerFunc) *Reddit {
	t.Helper()
	www := httptest.NewServer(token)
	oauth := httptest.NewServer(api)
	oldW, oldA := redditWWW, redditOAuth
	redditWWW, redditOAuth = www.URL, oauth.URL
	t.Cleanup(func() {
		redditWWW, redditOAuth = oldW, oldA
		www.Close()
		oauth.Close()
	})
	return &Reddit{Creds: RedditCredentials{ClientID: "id", ClientSecret: "cs-Qx7", RefreshToken: "rt-Zp9", Username: "me"}, Version: "test"}
}

func okToken(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"access_token":"at"}`)) }

func TestRedditCheck(t *testing.T) {
	ctx := context.Background()
	me := `{"name":"me","is_suspended":false}`
	api := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		switch me {
		case "429":
			w.Header().Set("x-ratelimit-reset", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "503":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Write([]byte(me))
		}
	}
	r := fakeReddit(t, okToken, api)
	if h, err := r.Check(ctx); err != nil || h.Status != HealthLive || h.Detail != "u/me" {
		t.Fatalf("live: %+v %v", h, err)
	}
	me = `{"name":"me","is_suspended":true}`
	if h, err := r.Check(ctx); err != nil || h.Status != HealthSuspended {
		t.Fatalf("suspended: %+v %v", h, err)
	}
	me = "429"
	if h, err := r.Check(ctx); err != nil || h.Status != HealthLimited || h.RetryAfter != 2*time.Minute {
		t.Fatalf("limited: %+v %v", h, err)
	}
	me = "503"
	if h, err := r.Check(ctx); err == nil || h.Status != "" {
		t.Fatalf("a 5xx must be an error, not a status: %+v %v", h, err)
	}
	if h, err := (&Reddit{}).Check(ctx); err != nil || h.Status != HealthInvalid {
		t.Fatalf("not connected: %+v %v", h, err)
	}
}

func TestRedditTokenRefusals(t *testing.T) {
	ctx := context.Background()
	tokenReply := ""
	tokenStatus := http.StatusOK
	r := fakeReddit(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(tokenStatus)
		w.Write([]byte(tokenReply))
	}, func(w http.ResponseWriter, r *http.Request) { t.Errorf("api called: %s", r.URL.Path) })
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusBadRequest, `{"error":"invalid_grant"}`, HealthInvalid},
		{http.StatusOK, `{"error":"invalid_grant"}`, HealthInvalid},
		{http.StatusUnauthorized, `{"message":"Unauthorized","error":401}`, HealthInvalid},
		{http.StatusTooManyRequests, ``, HealthLimited},
	} {
		tokenStatus, tokenReply = c.status, c.body
		h, err := r.Check(ctx)
		if err != nil || h.Status != c.want {
			t.Errorf("%d %s: %+v %v", c.status, c.body, h, err)
		}
		if strings.Contains(h.Detail, "cs-Qx7") || strings.Contains(h.Detail, "rt-Zp9") {
			t.Errorf("detail leaks a credential: %q", h.Detail)
		}
		_, err = r.Publish(ctx, Post{Kind: "post", Community: "golang", Title: "T", Body: "B"})
		if s, _ := Classify(err); s != c.want || strings.Contains(err.Error(), "cs-Qx7") || strings.Contains(err.Error(), "rt-Zp9") {
			t.Errorf("publish %d: %q %v", c.status, s, err)
		}
	}
	tokenStatus, tokenReply = http.StatusBadGateway, `bad gateway`
	if _, err := r.Check(ctx); err == nil {
		t.Error("a 502 from the token endpoint must be an error")
	}
}

func TestRedditRenewsExpiredToken(t *testing.T) {
	minted := 0
	r := fakeReddit(t, func(w http.ResponseWriter, r *http.Request) {
		minted++
		w.Write([]byte(`{"access_token":"fresh"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"name":"me"}`))
	})
	r.token = "expired"
	if h, err := r.Check(context.Background()); err != nil || h.Status != HealthLive || minted != 1 {
		t.Fatalf("%+v %v minted %d", h, err, minted)
	}
}

func TestRedditRateLimitEnvelope(t *testing.T) {
	cases := map[string]time.Duration{
		`{"json":{"errors":[["RATELIMIT","you are doing that too much. try again in 9 minutes.","ratelimit"]]}}`:                                                               9 * time.Minute,
		`{"json":{"errors":[["RATELIMIT","Looks like you've been doing that a lot. Take a break for 30 seconds before trying again. try again in 30 seconds.","ratelimit"]]}}`: 30 * time.Second,
		`{"json":{"errors":[["RATELIMIT","you are doing that too much. try again in 1 minute.","ratelimit"]]}}`:                                                                time.Minute,
		`{"json":{"ratelimit":125.5,"errors":[["RATELIMIT","you are doing that too much. try again in 2 minutes.","ratelimit"]]}}`:                                             125500 * time.Millisecond,
		`{"json":{"errors":[["RATELIMIT","you are doing that too much","ratelimit"]]}}`:                                                                                        defaultRetryAfter,
	}
	for body, want := range cases {
		var env redditJSON
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatal(err)
		}
		err := env.err()
		var rl *RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter != want || !strings.Contains(err.Error(), "RATELIMIT") {
			t.Errorf("%s: %v (want %s)", body, err, want)
		}
	}
	var env redditJSON
	json.Unmarshal([]byte(`{"json":{"errors":[["SUBREDDIT_NOTALLOWED","you aren't allowed to post there.","sr"]]}}`), &env)
	if err := env.err(); err == nil || strings.Contains(err.Error(), "rate") {
		t.Errorf("other errors stay plain: %v", err)
	} else if s, _ := Classify(err); s != "" {
		t.Errorf("classified %q", s)
	}
}

func TestRedditMetricsRemovedAndRules(t *testing.T) {
	info := map[string]string{
		"t3_ok":      `{"data":{"children":[{"data":{"score":5,"num_comments":1,"selftext":"hello","removed_by_category":null}}]}}`,
		"t3_mod":     `{"data":{"children":[{"data":{"score":1,"num_comments":0,"selftext":"hello","removed_by_category":"moderator"}}]}}`,
		"t3_removed": `{"data":{"children":[{"data":{"score":1,"selftext":"[removed]"}}]}}`,
		"t1_deleted": `{"data":{"children":[{"data":{"score":2,"body":"[deleted]"}}]}}`,
		"t1_gone":    `{"data":{"children":[]}}`,
	}
	r := fakeReddit(t, okToken, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/info":
			w.Write([]byte(info[r.URL.Query().Get("id")]))
		case "/r/golang/about/rules":
			w.Write([]byte(`{"rules":[{"kind":"all","short_name":"Be nice","description":"No flames."},
				{"kind":"link","short_name":"No spam","description":""}],"site_rules":["Spam"]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	for id, removed := range map[string]bool{"t3_ok": false, "t3_mod": true, "t3_removed": true, "t1_deleted": true, "t1_gone": true} {
		m, err := r.Metrics(ctx, id)
		if err != nil || (m[MetricRemoved] == true) != removed {
			t.Errorf("%s: %v %v", id, m, err)
		}
	}
	if m, _ := r.Metrics(ctx, "t3_ok"); m["score"] != int64(5) {
		t.Errorf("metrics %v", m)
	}
	for _, sub := range []string{"golang", "r/golang", "/r/golang/"} {
		rules, err := r.SubredditRules(ctx, sub)
		if err != nil || len(rules) != 2 || rules[0] != (SubredditRule{Name: "Be nice", Description: "No flames."}) || rules[1].Name != "No spam" {
			t.Errorf("%s: %+v %v", sub, rules, err)
		}
	}
	if _, err := r.SubredditRules(ctx, " r/ "); err == nil {
		t.Error("empty subreddit accepted")
	}
}

// fakeBluesky answers createSession with status and session, and getPosts
// with posts; tests change the fields between calls.
type fakeBluesky struct {
	t              *testing.T
	status         int
	session, posts string
}

func (f *fakeBluesky) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/xrpc/com.atproto.server.createSession":
		if f.status == http.StatusTooManyRequests {
			w.Header().Set("ratelimit-reset", "1790769900")
		}
		w.WriteHeader(f.status)
		w.Write([]byte(f.session))
	case "/xrpc/app.bsky.feed.getPosts":
		w.Write([]byte(f.posts))
	default:
		f.t.Errorf("unexpected %s", r.URL.Path)
	}
}

func TestBlueskyCheckAndRemoved(t *testing.T) {
	fake := &fakeBluesky{t: t, status: http.StatusOK,
		session: `{"accessJwt":"jwt","did":"did:plc:me","handle":"me.bsky.social","active":true}`,
		posts:   `{"posts":[{"likeCount":3,"repostCount":1,"replyCount":0,"quoteCount":0}]}`}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	pinClock(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	b := &Bluesky{Creds: BlueskyCredentials{Service: srv.URL, Identifier: "me", AppPassword: "app-password-secret"}}
	ctx := context.Background()

	if h, err := b.Check(ctx); err != nil || h.Status != HealthLive || h.Detail != "@me.bsky.social" {
		t.Fatalf("live: %+v %v", h, err)
	}
	if m, err := b.Metrics(ctx, "at://did:plc:me/app.bsky.feed.post/1"); err != nil || m["likes"] != int64(3) || m[MetricRemoved] != nil {
		t.Fatalf("metrics %v %v", m, err)
	}
	fake.posts = `{"posts":[]}`
	if m, err := b.Metrics(ctx, "at://did:plc:me/app.bsky.feed.post/1"); err != nil || m[MetricRemoved] != true {
		t.Fatalf("removed %v %v", m, err)
	}
	fake.status, fake.session = http.StatusBadGateway, `upstream failed`
	if _, err := b.Check(ctx); err == nil {
		t.Error("a 502 must be an error, not a status")
	}
}

func TestBlueskyAccountStatuses(t *testing.T) {
	fake := &fakeBluesky{t: t}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	pinClock(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	b := &Bluesky{Creds: BlueskyCredentials{Service: srv.URL, Identifier: "me", AppPassword: "app-password-secret"}}
	ctx := context.Background()
	for _, c := range []struct {
		status  int
		session string
		want    string
	}{
		{http.StatusUnauthorized, `{"error":"AuthenticationRequired","message":"Invalid identifier or password"}`, HealthInvalid},
		{http.StatusUnauthorized, `{"error":"AccountTakedown","message":"Account has been taken down"}`, HealthSuspended},
		{http.StatusBadRequest, `{"error":"AccountDeactivated","message":"Account is deactivated"}`, HealthSuspended},
		{http.StatusOK, `{"accessJwt":"jwt","did":"did:plc:me","handle":"me.bsky.social","active":false,"status":"deactivated"}`, HealthSuspended},
		{http.StatusOK, `{"accessJwt":"jwt","did":"did:plc:me","handle":"me.bsky.social","active":false,"status":"takendown"}`, HealthSuspended},
		{http.StatusTooManyRequests, `{"error":"RateLimitExceeded","message":"Rate Limit Exceeded"}`, HealthLimited},
	} {
		fake.status, fake.session = c.status, c.session
		h, err := b.Check(ctx)
		if err != nil || h.Status != c.want || strings.Contains(h.Detail, "secret") {
			t.Errorf("%d %s: %+v %v", c.status, c.session, h, err)
		}
		if c.want == HealthLimited && h.RetryAfter != 5*time.Minute {
			t.Errorf("retry after %s", h.RetryAfter)
		}
		_, err = b.Publish(ctx, Post{Kind: "post", Body: "hi"})
		if s, _ := Classify(err); s != c.want || strings.Contains(err.Error(), "secret") {
			t.Errorf("publish %d: %q %v", c.status, s, err)
		}
	}
}

// fakeMastodon answers verify_credentials with verify and has fixed answers
// for publishing (rate-limited) and for statuses 1 (live), 2 (gone) and 3
// (token revoked).
type fakeMastodon struct {
	t      *testing.T
	verify func(http.ResponseWriter)
}

func (f *fakeMastodon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/accounts/verify_credentials":
		f.verify(w)
	case r.URL.Path == "/api/v1/statuses" && r.Method == "POST":
		w.Header().Set("X-RateLimit-Reset", "2026-09-30T12:03:00.000Z")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"Too many requests"}`))
	case r.URL.Path == "/api/v1/statuses/1":
		w.Write([]byte(`{"favourites_count":2,"reblogs_count":1,"replies_count":0}`))
	case r.URL.Path == "/api/v1/statuses/2":
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Record not found"}`))
	case r.URL.Path == "/api/v1/statuses/3":
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"The access token is invalid"}`))
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}
}

func mastodonFixture(t *testing.T) (*Mastodon, *fakeMastodon) {
	t.Helper()
	fake := &fakeMastodon{t: t, verify: func(w http.ResponseWriter) { w.Write([]byte(`{"acct":"me"}`)) }}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	pinClock(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	return &Mastodon{Creds: MastodonCredentials{Instance: srv.URL, AccessToken: "masto-token-secret"}}, fake
}

func TestMastodonCheck(t *testing.T) {
	m, fake := mastodonFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		reply func(http.ResponseWriter)
		want  string
	}{
		{fake.verify, HealthLive},
		{func(w http.ResponseWriter) { w.Write([]byte(`{"acct":"me","suspended":true}`)) }, HealthSuspended},
		{func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"The access token is invalid"}`))
		}, HealthInvalid},
		{func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"Your login is currently disabled"}`))
		}, HealthSuspended},
	} {
		fake.verify = c.reply
		h, err := m.Check(ctx)
		if err != nil || h.Status != c.want || strings.Contains(h.Detail, "secret") {
			t.Errorf("want %s: %+v %v", c.want, h, err)
		}
	}
	fake.verify = func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }
	if _, err := m.Check(ctx); err == nil {
		t.Error("a 500 must be an error, not a status")
	}
}

func TestMastodonLimitsAndRemoved(t *testing.T) {
	m, _ := mastodonFixture(t)
	ctx := context.Background()
	_, err := m.Publish(ctx, Post{Kind: "post", Body: "hi"})
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 3*time.Minute {
		t.Fatalf("publish 429: %v", err)
	}
	if got, err := m.Metrics(ctx, "1"); err != nil || got["favourites"] != int64(2) || got[MetricRemoved] != nil {
		t.Fatalf("metrics %v %v", got, err)
	}
	if got, err := m.Metrics(ctx, "2"); err != nil || got[MetricRemoved] != true {
		t.Fatalf("removed %v %v", got, err)
	}
	if _, err := m.Metrics(ctx, "3"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("metrics 401: %v", err)
	} else if s, _ := Classify(err); s != HealthInvalid {
		t.Fatalf("metrics 401 classified %q", s)
	}
}

// fakeDevto accepts only the key devto-key-secret; the key "down" gets a 503.
func fakeDevto(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("api-key") {
		case "down":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "devto-key-secret":
			devtoRoutes(t, w, r)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized","status":401}`))
		}
	}))
	t.Cleanup(srv.Close)
	old := devtoAPI
	devtoAPI = srv.URL
	t.Cleanup(func() { devtoAPI = old })
}

func devtoRoutes(t *testing.T, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/users/me":
		w.Write([]byte(`{"username":"me_dev"}`))
	case "/articles/me/published":
		w.Write([]byte(`[{"id":1,"page_views_count":9,"public_reactions_count":2,"comments_count":1}]`))
	case "/articles/2":
		w.Write([]byte(`{"id":2,"public_reactions_count":5,"comments_count":3}`))
	case "/articles/3":
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found","status":404}`))
	default:
		t.Errorf("unexpected %s", r.URL.Path)
	}
}

func TestDevtoCheckAndRemoved(t *testing.T) {
	fakeDevto(t)
	ctx := context.Background()
	d := &Devto{Creds: DevtoCredentials{APIKey: "devto-key-secret"}}
	if h, err := d.Check(ctx); err != nil || h.Status != HealthLive || h.Detail != "me_dev" {
		t.Fatalf("live: %+v %v", h, err)
	}
	for id, want := range map[string]Metrics{
		"1": {"views": int64(9), "reactions": int64(2), "comments": int64(1)},
		"2": {"reactions": int64(5), "comments": int64(3)},
		"3": {MetricRemoved: true},
	} {
		got, err := d.Metrics(ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v %v, want %v", id, got, err, want)
		}
	}
}

func TestDevtoInvalidAndDown(t *testing.T) {
	fakeDevto(t)
	ctx := context.Background()
	bad := &Devto{Creds: DevtoCredentials{APIKey: "wrong"}}
	if h, err := bad.Check(ctx); err != nil || h.Status != HealthInvalid {
		t.Fatalf("invalid: %+v %v", h, err)
	}
	if _, err := bad.Publish(ctx, Post{Kind: "post", Title: "T", Body: "B"}); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("publish: %v", err)
	} else if s, _ := Classify(err); s != HealthInvalid {
		t.Fatalf("publish classified %q", s)
	}
	if _, err := (&Devto{Creds: DevtoCredentials{APIKey: "down"}}).Check(ctx); err == nil {
		t.Fatal("a 503 must be an error, not a status")
	}
}

func TestCheckTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing listens any more
	ctx := context.Background()
	checkers := map[string]Checker{
		"bluesky":  &Bluesky{Creds: BlueskyCredentials{Service: base}},
		"mastodon": &Mastodon{Creds: MastodonCredentials{Instance: base}},
	}
	for name, c := range checkers {
		if h, err := c.Check(ctx); err == nil || h.Status != "" {
			t.Errorf("%s: %+v %v", name, h, err)
		} else if s, _ := Classify(err); s != "" {
			t.Errorf("%s: a network error classified %q", name, s)
		}
	}
}

// The Reddit publish path must also turn a 200-with-RATELIMIT into a
// RateLimitError that Classify understands.
func TestRedditPublishRateLimited(t *testing.T) {
	r := fakeReddit(t, okToken, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if form, _ := url.ParseQuery(string(raw)); form.Get("sr") != "golang" {
			t.Errorf("form %v", form)
		}
		w.Write([]byte(`{"json":{"errors":[["RATELIMIT","you are doing that too much. try again in 9 minutes.","ratelimit"]]}}`))
	})
	_, err := r.Publish(context.Background(), Post{Kind: "post", Community: "r/golang", Title: "T", Body: "B"})
	if s, wait := Classify(err); s != HealthLimited || wait != 9*time.Minute {
		t.Fatalf("%q %s %v", s, wait, err)
	}
}

func TestDefaultLimits(t *testing.T) {
	for platform, want := range map[string]Limits{
		"reddit":   {Daily: 5, MinInterval: 10 * time.Minute, CommunityCooldown: 24 * time.Hour},
		"bluesky":  {Daily: 20, MinInterval: 2 * time.Minute},
		"mastodon": {Daily: 20, MinInterval: 2 * time.Minute},
		"devto":    {Daily: 2, MinInterval: time.Hour},
		"other":    {Daily: 10, MinInterval: 5 * time.Minute},
	} {
		if got := DefaultLimits(platform); got != want {
			t.Errorf("%s: %+v, want %+v", platform, got, want)
		}
	}
}

// A Dev.to metrics call the server cannot answer is an error, not a removal.
func TestDevtoMetricsServerError(t *testing.T) {
	fakeDevto(t)
	if _, err := (&Devto{Creds: DevtoCredentials{APIKey: "down"}}).Metrics(context.Background(), "2"); err == nil || isNotFound(err) {
		t.Fatalf("metrics 503: %v", err)
	}
}
