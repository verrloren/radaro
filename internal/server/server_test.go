package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/store"
)

func init() { auth.FastHashingForTests() }

// newServer returns the API with every request signed in as the first user
// (the admin), unless the request sets its own Authorization header.
func newServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	srv, st := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	return signedIn(t, srv, "owner@example.com"), st
}

// signedIn registers email (open registration is not needed for the first
// user) and wraps the handler so requests carry that user's token.
func signedIn(t *testing.T, srv *Server, email string) http.Handler {
	t.Helper()
	sess, err := srv.auth.Register(email, "correct horse", "test")
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+sess.AccessToken)
		}
		h.ServeHTTP(w, r)
	})
}

func newTestServer(t *testing.T, cfg *config.Config) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &model.Mention{Source: "hackernews", Query: "go", Text: "great", URL: model.Str("https://1"),
		CreatedAt: time.Now(), Sentiment: model.Positive}
	m.Normalize()
	// Unowned data: the first user to register takes it over.
	if err := st.SaveTracking(0, "go", []string{"hackernews"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upsert([]*model.Mention{m}, true); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL, cfg.RefreshTTL = 15*time.Minute, time.Hour
	}
	srv, err := New(cfg, st, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func do(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReadEndpoints(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(h, "GET", "/health", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("health %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", "/api/mentions?q=go", "")
	var ms []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ms); err != nil || len(ms) != 1 || ms[0]["source_label"] != "Hacker News" {
		t.Fatalf("mentions %s", rec.Body)
	}
	if rec := do(h, "GET", "/api/mentions?sentiment=angry", ""); rec.Code != 422 {
		t.Fatalf("bad sentiment → %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/summary?q=go&p=1", ""); rec.Code != 422 {
		t.Fatalf("q+p → %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/summary?p=99", ""); rec.Code != 404 {
		t.Fatalf("unknown project → %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/tracking?q=nope", ""); rec.Code != 404 {
		t.Fatalf("unknown tracking → %d", rec.Code)
	}
}

func TestProjectEndpoints(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(h, "POST", "/api/projects", `{"name":"Lang"}`); rec.Code != 201 {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/projects", `{"name":"lang"}`); rec.Code != 409 {
		t.Fatalf("duplicate %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/projects/2/queries", `{"query":"go"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"added":true`) {
		t.Fatalf("add %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", "/api/projects/1", ""); rec.Code != 409 {
		t.Fatalf("delete default %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/projects/2/queries", `{"query":"go"}`); rec.Code != 200 {
		t.Fatalf("remove %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/projects/2", ""); rec.Code != 200 {
		t.Fatalf("delete %d", rec.Code)
	}
}

func TestCrossOriginRejected(t *testing.T) {
	h, _ := newServer(t)
	rec := do(h, "POST", "/api/projects", `{"name":"x"}`, "Origin", "http://evil.example")
	if rec.Code != 403 {
		t.Fatalf("cross-origin → %d", rec.Code)
	}
	rec = do(h, "POST", "/api/projects", `{"name":"x"}`, "Origin", "http://example.com") // httptest Host
	if rec.Code != 201 {
		t.Fatalf("same-origin → %d %s", rec.Code, rec.Body)
	}
}

func TestTrackValidation(t *testing.T) {
	h, _ := newServer(t)
	for _, body := range []string{`{"query":"","sources":["hackernews"]}`, `{"query":"x","sources":["nope"]}`,
		`{"query":"x","sources":[]}`, `{"query":"x","sources":["hackernews"],"mode":"sideways"}`} {
		if rec := do(h, "POST", "/api/track", body); rec.Code != 422 {
			t.Fatalf("%s → %d", body, rec.Code)
		}
	}
}

func TestSPAFallback(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(h, "GET", "/some/client/route", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("fallback %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/assets/app.js", ""); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("hashed assets should be cached")
	}
	if rec := do(h, "GET", "/api/unknown", ""); rec.Code != 404 {
		t.Fatalf("unknown api → %d", rec.Code)
	}
}
