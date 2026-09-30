package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/config"
)

var publicRoutes = map[string]bool{
	"GET /health":               true,
	"GET " + redditCallbackPath: true, // the single-use state is the credential
	"GET /api/auth/config":      true,
	"POST /api/auth/register":   true,
	"POST /api/auth/login":      true,
	"POST /api/auth/refresh":    true,
	"POST /api/auth/logout":     true,
}

// Every route that is not deliberately public must answer 401 without a token.
func TestEveryAPIRouteRequiresAuth(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	h := srv.Handler()
	routes, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("handler is not a chi router")
	}
	checked := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if publicRoutes[key] {
			return nil
		}
		path := strings.NewReplacer("{id}", "1", "{name}", "reddit", "{kid}", "1", "{platform}", "reddit").Replace(route)
		rec := do(h, method, path, "{}")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s → %d without a token, want 401", key, rec.Code)
		}
		for _, bad := range []string{"Bearer nope", "Basic abc"} {
			if rec := do(h, method, path, "{}", "Authorization", bad); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with %q → %d, want 401", key, bad, rec.Code)
			}
		}
		checked++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 15 {
		t.Fatalf("only %d protected routes found; the walk is broken", checked)
	}
}

func cookies(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		out[c.Name] = c
	}
	return out
}

func TestBrowserSessionWithCookies(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	h := srv.Handler()
	if rec := do(h, "GET", "/api/auth/config", ""); !strings.Contains(rec.Body.String(), `"registration_open":true`) {
		t.Fatalf("config before any user: %s", rec.Body)
	}
	rec := do(h, "POST", "/api/auth/register", `{"email":"Owner@Example.com","password":"correct horse"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("a browser response exposed a token: %s", rec.Body)
	}
	c := cookies(rec)
	access, refresh := c[accessCookie], c[refreshCookie]
	if access == nil || refresh == nil || !access.HttpOnly || !refresh.HttpOnly || access.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v", c)
	}
	if access.Secure {
		t.Fatal("Secure cookie over plain HTTP would never be sent back")
	}
	if refresh.Path != "/api/auth" {
		t.Fatalf("refresh cookie path = %q", refresh.Path)
	}
	withCookie := func(method, path string, cs ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		for _, c := range cs {
			req.AddCookie(c)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	if rec := withCookie("GET", "/api/auth/me", access); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"owner@example.com"`) ||
		strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("me %d %s", rec.Code, rec.Body)
	}
	if rec := withCookie("GET", "/api/projects", access); rec.Code != 200 {
		t.Fatalf("projects with cookie %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/auth/config", ""); !strings.Contains(rec.Body.String(), `"registration_open":false`) {
		t.Fatalf("config after the first user: %s", rec.Body)
	}

	rec = withCookie("POST", "/api/auth/refresh", refresh)
	if rec.Code != 200 {
		t.Fatalf("refresh %d %s", rec.Code, rec.Body)
	}
	next := cookies(rec)[refreshCookie]
	if next == nil || next.Value == refresh.Value {
		t.Fatal("refresh did not rotate the cookie")
	}
	rec = withCookie("POST", "/api/auth/logout", next)
	if rec.Code != 200 || cookies(rec)[accessCookie].MaxAge >= 0 {
		t.Fatalf("logout %d, cookies %+v", rec.Code, cookies(rec))
	}
	if rec := withCookie("POST", "/api/auth/refresh", next); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout %d", rec.Code)
	}
}

func TestSecureCookiesBehindHTTPSProxy(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	rec := do(srv.Handler(), "POST", "/api/auth/register", `{"email":"a@example.com","password":"correct horse"}`,
		"X-Forwarded-Proto", "https")
	if c := cookies(rec)[accessCookie]; c == nil || !c.Secure {
		t.Fatalf("cookie behind an HTTPS proxy = %+v", c)
	}
}

func TestCLISessionWithBearerTokens(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	h := srv.Handler()
	cli := []string{cliHeader, "cli"}
	rec := do(h, "POST", "/api/auth/register", `{"email":"a@example.com","password":"correct horse"}`, cli...)
	var sess struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil || sess.AccessToken == "" || sess.RefreshToken == "" {
		t.Fatalf("register %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("the CLI got cookies")
	}
	if rec := do(h, "GET", "/api/projects", "", "Authorization", "Bearer "+sess.AccessToken); rec.Code != 200 {
		t.Fatalf("bearer request %d", rec.Code)
	}
	rec = do(h, "POST", "/api/auth/refresh", `{"refresh_token":"`+sess.RefreshToken+`"}`, cli...)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "refresh_token") {
		t.Fatalf("refresh %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/auth/login", `{"email":"a@example.com","password":"wrong password"}`, cli...); rec.Code != 401 {
		t.Fatalf("wrong password %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/auth/login", `{"email":"nobody@example.com","password":"correct horse"}`, cli...); rec.Code != 401 ||
		!strings.Contains(rec.Body.String(), "invalid email or password") {
		t.Fatalf("unknown email %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/auth/register", `{"email":"a@example.com","password":"correct horse"}`, cli...); rec.Code != 409 {
		t.Fatalf("duplicate %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/auth/register", `{"email":"b@example.com","password":"short"}`, cli...); rec.Code != 422 {
		t.Fatalf("short password %d", rec.Code)
	}
}

func TestClosedRegistration(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	h := srv.Handler()
	do(h, "POST", "/api/auth/register", `{"email":"a@example.com","password":"correct horse"}`)
	if rec := do(h, "POST", "/api/auth/register", `{"email":"b@example.com","password":"correct horse"}`); rec.Code != 403 {
		t.Fatalf("second sign-up on a closed server %d %s", rec.Code, rec.Body)
	}
}

func TestSignInIsRateLimited(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	h := srv.Handler()
	codes := map[int]int{}
	for range 12 {
		codes[do(h, "POST", "/api/auth/login", `{"email":"a@example.com","password":"wrong password"}`).Code]++
	}
	if codes[401] != 10 || codes[429] != 2 {
		t.Fatalf("codes = %v, want 10×401 then 429", codes)
	}
	// Another client behind the same local proxy has its own budget.
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"a@example.com","password":"x"}`))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == 429 {
		t.Fatal("a different client behind the proxy was limited")
	}
}

func TestLimiterRefills(t *testing.T) {
	l := newLimiter(60, 1)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.allow("a") || l.allow("a") {
		t.Fatal("burst of 1 not enforced")
	}
	now = now.Add(time.Second)
	if !l.allow("a") {
		t.Fatal("bucket did not refill")
	}
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct{ remote, xff, want string }{
		{"198.51.100.1:1234", "", "198.51.100.1"},
		{"198.51.100.1:1234", "1.2.3.4", "198.51.100.1"}, // not from a local proxy: ignored
		{"127.0.0.1:1234", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"[::1]:1234", "", "::1"},
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(req); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestInstanceSettingsNeedTheAdmin(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	signedIn(t, srv, "admin@example.com")
	h := signedIn(t, srv, "member@example.com")
	if rec := do(h, "PUT", "/api/settings/sources/x", `{"values":{"bearer_token":"t"}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("member changed instance settings: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/settings/sources", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "bearer_token\":\"t") {
		t.Fatalf("member list %d", rec.Code)
	}
}
