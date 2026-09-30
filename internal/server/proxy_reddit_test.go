package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/netproxy"
	"github.com/verrloren/radaro/internal/publish"
)

func TestProxySettingsAreAdminOnlyAndApplyImmediately(t *testing.T) {
	defer netproxy.Set("")
	srv, st := newTestServer(t, &config.Config{Registration: "open"})
	admin := signedIn(t, srv, "admin@example.com")
	other := signedIn(t, srv, "other@example.com")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through proxy")
	}))
	defer proxy.Close()
	raw := strings.Replace(proxy.URL, "http://", "http://user:private@", 1)
	if rec := do(other, "PUT", "/api/settings/proxy", fmt.Sprintf(`{"url":%q}`, raw)); rec.Code != 403 {
		t.Fatalf("non-admin changed proxy: %d", rec.Code)
	}
	if rec := do(admin, "PUT", "/api/settings/proxy", `{"url":"bad"}`); rec.Code != 422 || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("invalid proxy: %d %s", rec.Code, rec.Body)
	}
	if rec := do(admin, "PUT", "/api/settings/proxy", fmt.Sprintf(`{"url":%q}`, raw)); rec.Code != 200 || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("saved proxy: %d %s", rec.Code, rec.Body)
	}
	if saved, err := st.ProxyURL(); err != nil || saved != raw {
		t.Fatal("proxy was not persisted")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "direct") }))
	defer target.Close()
	res, err := (&http.Client{Transport: netproxy.Transport()}).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "through proxy" {
		t.Fatalf("proxy was not used: %q", body)
	}
	if rec := do(other, "GET", "/api/settings/proxy", ""); rec.Code != 403 {
		t.Fatalf("non-admin read proxy settings: %d", rec.Code)
	}
	if rec := do(admin, "DELETE", "/api/settings/proxy", ""); rec.Code != 200 {
		t.Fatalf("delete proxy: %d %s", rec.Code, rec.Body)
	}
	if saved, err := st.ProxyURL(); err != nil || saved != "" {
		t.Fatal("proxy override was not cleared")
	}
}

func TestRedditAppCanBeReusedOnlyByOwner(t *testing.T) {
	srv, st := newTestServer(t, &config.Config{Registration: "open"})
	a := signedIn(t, srv, "a@example.com")
	b := signedIn(t, srv, "b@example.com")
	if rec := do(a, "POST", "/api/accounts/reddit/authorize", `{}`); rec.Code != 422 {
		t.Fatalf("empty app: %d %s", rec.Code, rec.Body)
	}
	creds := publish.RedditCredentials{ClientID: "client-id", ClientSecret: "private", RefreshToken: "refresh", Username: "alice"}
	if _, err := st.SaveAccount(1, "reddit", "alice", creds); err != nil {
		t.Fatal(err)
	}
	if rec := do(b, "GET", "/api/accounts/reddit/app", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("other user sees app: %d %s", rec.Code, rec.Body)
	}
	if rec := do(b, "POST", "/api/accounts/reddit/authorize", `{}`); rec.Code != 422 {
		t.Fatalf("other user reused app: %d %s", rec.Code, rec.Body)
	}
	rec := do(a, "POST", "/api/accounts/reddit/authorize", `{}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("reuse app: %d %s", rec.Code, rec.Body)
	}
	var answer struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || !strings.Contains(answer.AuthorizeURL, "client_id=client-id") {
		t.Fatalf("wrong authorize URL: %s", rec.Body)
	}
}
