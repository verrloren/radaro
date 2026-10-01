package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
)

type fakeBrowserLogin struct {
	username, password, proxy string
	confirmed, closed         bool
	last                      redditbrowser.Input
}

func (f *fakeBrowserLogin) Login(_ context.Context, u, p string) error {
	f.username, f.password = u, p
	return nil
}
func (f *fakeBrowserLogin) Screenshot(context.Context) (redditbrowser.Screen, error) {
	return redditbrowser.Screen{Image: "png", Width: 1000, Height: 720}, nil
}
func (f *fakeBrowserLogin) Input(_ context.Context, in redditbrowser.Input) error {
	f.last = in
	return nil
}
func (f *fakeBrowserLogin) Finish(context.Context) (*redditbrowser.Credentials, error) {
	if !f.confirmed {
		return nil, redditbrowser.ErrChallenge
	}
	return &redditbrowser.Credentials{Username: "alice", Proxy: f.proxy, Cookies: []*network.CookieParam{{Name: "reddit_session", Value: "private-cookie", Domain: ".reddit.com", Path: "/"}}}, nil
}
func (f *fakeBrowserLogin) Close() { f.closed = true }

func TestRedditBrowserLoginIsPrivateAndRequiresConfirmation(t *testing.T) {
	s, st := newTestServer(t, &config.Config{Registration: "open"})
	a := signedIn(t, s, "a@example.com")
	b := signedIn(t, s, "b@example.com")
	u, _ := st.UserByEmail("a@example.com")
	project, _ := st.CreateProject(u.ID, "Launch")
	fake := &fakeBrowserLogin{}
	s.openRedditBrowser = func(_ context.Context, p string, _ bool) (redditBrowser, error) { fake.proxy = p; return fake, nil }
	body, _ := json.Marshal(map[string]any{"username": "login-email", "password": "private-password", "proxy_url": "http://proxy-user:private-proxy@127.0.0.1:9090", "project_id": project.ID})
	rec := do(a, "POST", "/api/accounts/reddit/browser", string(body))
	if rec.Code != 201 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	path := "/api/accounts/reddit/browser/" + started.SessionID
	for _, secret := range []string{"private-password", "private-proxy", "private-cookie"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("start exposed credentials")
		}
	}
	if fake.username != "login-email" || fake.password != "private-password" {
		t.Fatal("login data was not passed to Chromium")
	}
	for _, route := range []struct{ method, path, body string }{{"POST", path + "/input", `{"kind":"refresh"}`}, {"POST", path + "/finish", `{}`}, {"DELETE", path, `{}`}} {
		if rec := do(b, route.method, route.path, route.body); rec.Code != 404 {
			t.Fatalf("another user accessed sign-in: %d", rec.Code)
		}
	}
	if rec := do(a, "POST", path+"/finish", `{}`); rec.Code != 409 {
		t.Fatalf("unconfirmed login saved: %d", rec.Code)
	}
	if accs, _ := st.Accounts(u.ID, ""); len(accs) != 0 {
		t.Fatal("unconfirmed account was stored")
	}
	if rec := do(a, "POST", path+"/input", `{"kind":"text","text":"123456"}`); rec.Code != 200 || fake.last.Text != "123456" {
		t.Fatal("verification input was not forwarded")
	}
	fake.confirmed = true
	rec = do(a, "POST", path+"/finish", `{}`)
	if rec.Code != 201 || !fake.closed {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	for _, secret := range []string{"private-password", "private-proxy", "private-cookie"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("finish exposed credentials")
		}
	}
	accs, _ := st.Accounts(u.ID, "reddit")
	if len(accs) != 1 || accs[0].Status != "live" {
		t.Fatal("verified account was not saved")
	}
	var creds publish.RedditCredentials
	_ = json.Unmarshal(accs[0].Credentials, &creds)
	if creds.Browser == nil || creds.Browser.Proxy != fake.proxy || strings.Contains(string(accs[0].Credentials), "private-password") {
		t.Fatal("session/proxy storage is incorrect")
	}
	bindings, _ := st.ProjectBindings(u.ID, project.ID)
	if len(bindings) != 1 || bindings[0].Account.ID != accs[0].ID {
		t.Fatal("project binding is missing")
	}
	if rec := do(a, "POST", path+"/finish", `{}`); rec.Code != 404 {
		t.Fatal("completed sign-in was reused")
	}
	if rec := do(b, "PUT", "/api/accounts/1/browser/proxy", `{"proxy_url":""}`); rec.Code != 404 {
		t.Fatal("another user changed the proxy")
	}
	if rec := do(a, "PUT", "/api/accounts/1/browser/proxy", `{"proxy_url":"invalid-private-proxy"}`); rec.Code != 422 || strings.Contains(rec.Body.String(), "invalid-private-proxy") {
		t.Fatal("invalid proxy was accepted or leaked")
	}
	if rec := do(a, "PUT", "/api/accounts/1/browser/proxy", `{"proxy_url":"socks5://user:second-private@localhost:1080"}`); rec.Code != 200 || strings.Contains(rec.Body.String(), "second-private") {
		t.Fatal("proxy update failed or leaked")
	}
	if rec := do(a, "GET", "/api/accounts", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"proxy_configured":true`) || strings.Contains(rec.Body.String(), "second-private") {
		t.Fatal("account view leaked a proxy or missed its status")
	}
}

func TestRedditBrowserCancelExpiryAndOwnership(t *testing.T) {
	s, _ := newTestServer(t, &config.Config{Registration: "open"})
	h := signedIn(t, s, "a@example.com")
	var opened []*fakeBrowserLogin
	s.openRedditBrowser = func(_ context.Context, p string, _ bool) (redditBrowser, error) {
		f := &fakeBrowserLogin{proxy: p}
		opened = append(opened, f)
		return f, nil
	}
	start := func() string {
		rec := do(h, "POST", "/api/accounts/reddit/browser", `{"username":"a","password":"p"}`)
		if rec.Code != 201 {
			t.Fatalf("start: %d", rec.Code)
		}
		var d map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		var id string
		_ = json.Unmarshal(d["session_id"], &id)
		return id
	}
	id := start()
	id2 := start()
	if rec := do(h, "POST", "/api/accounts/reddit/browser", `{"username":"a","password":"p"}`); rec.Code != 429 {
		t.Fatal("unbounded login sessions")
	}
	if rec := do(h, "DELETE", "/api/accounts/reddit/browser/"+id, ""); rec.Code != 200 || !opened[0].closed {
		t.Fatal("cancel did not close browser")
	}
	s.browserLogins.Lock()
	s.browserLogins.sessions[id2].expires = time.Now().Add(-time.Second)
	s.browserLogins.Unlock()
	if rec := do(h, "POST", "/api/accounts/reddit/browser/"+id2+"/input", `{"kind":"refresh"}`); rec.Code != 404 || !opened[1].closed {
		t.Fatal("expired session remained usable")
	}
	for _, body := range []string{`{}`, `{"username":"a","password":"p","proxy_url":"private-invalid"}`, `{"username":"a","password":"p","project_id":999}`} {
		rec := do(h, "POST", "/api/accounts/reddit/browser", body)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != 404 {
			t.Fatalf("invalid start: %d", rec.Code)
		}
	}
}
