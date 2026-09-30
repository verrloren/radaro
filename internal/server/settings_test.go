package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/sources"
)

func sourceView(t *testing.T, body []byte) sourceSettingsView {
	t.Helper()
	var v sourceSettingsView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func field(v sourceSettingsView, key string) fieldView {
	for _, f := range v.Fields {
		if f.Key == key {
			return f
		}
	}
	return fieldView{}
}

func TestSourceSettings(t *testing.T) {
	cfg := &config.Config{Sources: []string{"hackernews"}, SourceOptions: sources.Options{YouTubeAPIKey: "env-yt-key"}}
	srv, _ := newTestServer(t, cfg)
	h := signedIn(t, srv, "owner@example.com")

	rec := do(h, "GET", "/api/settings/sources", "")
	var all []sourceSettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil || len(all) != 3 {
		t.Fatalf("list %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "env-yt-key") {
		t.Fatal("an environment secret leaked")
	}
	for _, v := range all {
		if v.Name == "youtube" && (!v.Configured || field(v, "api_key").Origin != "env") {
			t.Fatalf("youtube from env %+v", v)
		}
	}

	rec = do(h, "PUT", "/api/settings/sources/x", `{"values":{"bearer_token":"ui-secret"}}`)
	v := sourceView(t, rec.Body.Bytes())
	if rec.Code != 200 || !v.Configured || !v.Saved || field(v, "bearer_token").Origin != "ui" || strings.Contains(rec.Body.String(), "ui-secret") {
		t.Fatalf("save %d %s", rec.Code, rec.Body)
	}
	if rec = do(h, "GET", "/api/meta", ""); !strings.Contains(rec.Body.String(), `"name":"x","label":"X / Twitter","glyph":"X","color":"#d8dce5","needs_config":true,"configured":true`) {
		t.Fatalf("meta does not see the saved key: %s", rec.Body)
	}

	// A blank secret keeps the saved one.
	rec = do(h, "PUT", "/api/settings/sources/x", `{"values":{"bearer_token":""}}`)
	if v = sourceView(t, rec.Body.Bytes()); !v.Configured || field(v, "bearer_token").Value != "" {
		t.Fatalf("blank secret %s", rec.Body)
	}
	if opts, _ := srv.store.SourceSettings(); opts["x"]["bearer_token"] != "ui-secret" {
		t.Fatalf("secret not kept: %v", opts)
	}

	rec = do(h, "PUT", "/api/settings/sources/rss", `{"values":{"feeds":"https://a/feed, https://b/feed\n"}}`)
	if v = sourceView(t, rec.Body.Bytes()); field(v, "feeds").Value != "https://a/feed\nhttps://b/feed" {
		t.Fatalf("rss %s", rec.Body)
	}

	if rec = do(h, "PUT", "/api/settings/sources/x", `{"values":{"nope":"1"}}`); rec.Code != 422 {
		t.Fatalf("unknown field %d", rec.Code)
	}
	for _, name := range []string{"hackernews", "reddit", "mastodon"} {
		if rec = do(h, "PUT", "/api/settings/sources/"+name, `{"values":{}}`); rec.Code != 404 {
			t.Fatalf("%s has no settings form: %d", name, rec.Code)
		}
	}
	if rec = do(h, "PUT", "/api/settings/sources/x", `{"values":{"bearer_token":"z"}}`, "Origin", "https://evil.example"); rec.Code != 403 {
		t.Fatalf("cross-origin %d", rec.Code)
	}

	rec = do(h, "DELETE", "/api/settings/sources/x", "")
	if v = sourceView(t, rec.Body.Bytes()); v.Configured || v.Saved {
		t.Fatalf("reset %s", rec.Body)
	}
}

func TestAccountEndpoints(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	srv.connect = func(_ context.Context, platform string, in publish.ConnectInput) (string, any, error) {
		if in.Secret != "good-key" {
			return "", nil, errors.New("dev.to rejected the key: HTTP 401")
		}
		return "me", publish.DevtoCredentials{APIKey: in.Secret}, nil
	}
	h := signedIn(t, srv, "owner@example.com")

	if rec := do(h, "POST", "/api/accounts", `{"platform":"devto","secret":"bad"}`); rec.Code != 422 || !strings.Contains(rec.Body.String(), "rejected") {
		t.Fatalf("bad key %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/accounts", `{"platform":"myspace","secret":"x"}`); rec.Code != 422 {
		t.Fatalf("unknown platform %d", rec.Code)
	}
	rec := do(h, "POST", "/api/accounts", `{"platform":"devto","secret":"good-key"}`)
	if rec.Code != 201 || strings.Contains(rec.Body.String(), "good-key") {
		t.Fatalf("connect %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "GET", "/api/accounts", "")
	var list struct {
		Platforms []publish.Platform `json:"platforms"`
		Accounts  []struct {
			ID       int64  `json:"id"`
			Platform string `json:"platform"`
			Handle   string `json:"handle"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Platforms) != 4 || len(list.Accounts) != 1 ||
		list.Accounts[0].Handle != "me" || strings.Contains(rec.Body.String(), "good-key") {
		t.Fatalf("accounts %s", rec.Body)
	}
	id := list.Accounts[0].ID
	if rec = do(h, "DELETE", "/api/accounts/"+jsonNum(id), ""); rec.Code != 200 {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec = do(h, "DELETE", "/api/accounts/"+jsonNum(id), ""); rec.Code != 404 {
		t.Fatalf("delete twice %d", rec.Code)
	}
}

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestRedditOAuth(t *testing.T) {
	srv, st := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	var gotCode string
	srv.redditExchange = func(_ context.Context, r *publish.Reddit, code string) error {
		gotCode = code
		r.Creds.RefreshToken, r.Creds.Username = "refresh", "spez"
		return nil
	}
	h := signedIn(t, srv, "owner@example.com")

	if rec := do(h, "POST", "/api/accounts/reddit/authorize", `{"client_id":""}`); rec.Code != 422 {
		t.Fatalf("missing client id %d", rec.Code)
	}
	rec := do(h, "POST", "/api/accounts/reddit/authorize", `{"client_id":"cid","client_secret":"sec"}`)
	var auth struct {
		AuthorizeURL string `json:"authorize_url"`
		RedirectURI  string `json:"redirect_uri"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &auth); err != nil || auth.RedirectURI != "http://example.com/oauth/reddit/callback" {
		t.Fatalf("authorize %d %s", rec.Code, rec.Body)
	}
	u, _ := url.Parse(auth.AuthorizeURL)
	state := u.Query().Get("state")
	if state == "" || u.Query().Get("redirect_uri") != auth.RedirectURI || strings.Contains(auth.AuthorizeURL, "sec") {
		t.Fatalf("authorize url %s", auth.AuthorizeURL)
	}

	if rec = do(h, "GET", "/oauth/reddit/callback?state=wrong&code=c", ""); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "connect_error=") {
		t.Fatalf("bad state %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(h, "GET", "/oauth/reddit/callback?state="+state+"&code=the-code", "")
	if rec.Code != 303 || rec.Header().Get("Location") != "/?v=setup&connected=reddit" || gotCode != "the-code" {
		t.Fatalf("callback %d %s", rec.Code, rec.Header().Get("Location"))
	}
	accs, _ := st.Accounts("reddit")
	if len(accs) != 1 || accs[0].Handle != "spez" || !strings.Contains(string(accs[0].Credentials), `"refresh_token":"refresh"`) {
		t.Fatalf("saved %+v", accs)
	}
	if rec = do(h, "GET", "/oauth/reddit/callback?state="+state+"&code=again", ""); !strings.Contains(rec.Header().Get("Location"), "connect_error=") {
		t.Fatal("a state was accepted twice")
	}

	rec = do(h, "POST", "/api/accounts/reddit/authorize", `{"client_id":"cid"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &auth)
	u, _ = url.Parse(auth.AuthorizeURL)
	rec = do(h, "GET", "/oauth/reddit/callback?state="+u.Query().Get("state")+"&error=access_denied", "")
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "access_denied") {
		t.Fatalf("denied %s", loc)
	}
}
