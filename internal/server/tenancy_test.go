package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
)

// B must not read or change anything of A's through any endpoint.
func TestUsersCannotReachEachOthersData(t *testing.T) {
	srv, st := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	srv.connect = func(_ context.Context, platform string, in publish.ConnectInput) (string, any, error) {
		return "handle-" + in.Secret, map[string]string{"k": in.Secret}, nil
	}
	a := signedIn(t, srv, "a@example.com") // first user: owns the seeded "go" keyword
	b := signedIn(t, srv, "b@example.com")

	rec := do(a, "POST", "/api/projects", `{"name":"Secret launch"}`)
	var p struct{ ID int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.ID == 0 {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := do(a, "POST", fmt.Sprintf("/api/projects/%d/queries", p.ID), `{"query":"go"}`); rec.Code != 200 {
		t.Fatalf("add %d %s", rec.Code, rec.Body)
	}
	rec = do(a, "POST", "/api/accounts", `{"platform":"devto","secret":"k1"}`)
	var acc struct{ ID int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || acc.ID == 0 {
		t.Fatalf("connect %d %s", rec.Code, rec.Body)
	}

	pid := fmt.Sprint(p.ID)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/projects/" + pid, ""},
		{"DELETE", "/api/projects/" + pid, ""},
		{"POST", "/api/projects/" + pid + "/queries", `{"query":"go"}`},
		{"DELETE", "/api/projects/" + pid + "/queries", `{"query":"go"}`},
		{"GET", "/api/summary?p=" + pid, ""},
		{"GET", "/api/mentions?p=" + pid, ""},
		{"GET", "/api/queries?p=" + pid, ""},
		{"GET", "/api/tracking?q=go", ""},
		{"POST", "/api/track", `{"query":"x","sources":["hackernews"],"project_id":` + pid + `}`},
		{"DELETE", fmt.Sprintf("/api/accounts/%d", acc.ID), ""},
	} {
		if rec := do(b, c.method, c.path, c.body); rec.Code != 404 {
			t.Errorf("B %s %s → %d %s, want 404", c.method, c.path, rec.Code, rec.Body)
		}
	}
	for path, leak := range map[string]string{
		"/api/projects":      "Secret launch",
		"/api/queries":       `"go"`,
		"/api/mentions?q=go": "great",
		"/api/summary":       `"total":1`,
		"/api/accounts":      "handle-k1",
	} {
		if rec := do(b, "GET", path, ""); rec.Code != 200 || strings.Contains(rec.Body.String(), leak) {
			t.Errorf("B GET %s → %d leaks %q: %s", path, rec.Code, leak, rec.Body)
		}
	}
	// A still has everything.
	if rec := do(a, "GET", "/api/mentions?p="+pid, ""); !strings.Contains(rec.Body.String(), "great") {
		t.Fatalf("A lost access: %s", rec.Body)
	}
	if list, _ := st.Accounts(0, ""); len(list) != 1 {
		t.Fatal("B's attempts changed A's accounts")
	}
}
