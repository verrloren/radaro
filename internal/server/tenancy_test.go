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
	if rec := do(a, "POST", fmt.Sprintf("/api/projects/%d/keywords", p.ID), `{"query":"go"}`); rec.Code != 201 {
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
		{"PATCH", "/api/projects/" + pid, `{"name":"mine"}`},
		{"GET", "/api/projects/" + pid + "/keywords", ""},
		{"POST", "/api/projects/" + pid + "/keywords", `{"query":"go"}`},
		{"DELETE", "/api/projects/" + pid + "/keywords/1", ""},
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

func TestProjectAccountBindings(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	srv.connect = func(_ context.Context, platform string, in publish.ConnectInput) (string, any, error) {
		return in.Handle, map[string]string{"k": in.Secret}, nil
	}
	h := signedIn(t, srv, "a@example.com")
	other := signedIn(t, srv, "b@example.com")
	id := func(body []byte) int64 {
		var v struct{ ID int64 }
		json.Unmarshal(body, &v)
		return v.ID
	}
	p1 := id(do(h, "POST", "/api/projects", `{"name":"One"}`).Body.Bytes())
	p2 := id(do(h, "POST", "/api/projects", `{"name":"Two"}`).Body.Bytes())
	// Connecting with project_id binds right away.
	rec := do(h, "POST", "/api/accounts", fmt.Sprintf(`{"platform":"mastodon","handle":"first","secret":"s1","project_id":%d}`, p1))
	first := id(rec.Body.Bytes())
	if rec.Code != 201 || first == 0 {
		t.Fatalf("connect %d %s", rec.Code, rec.Body)
	}
	second := id(do(h, "POST", "/api/accounts", `{"platform":"mastodon","handle":"second","secret":"s2"}`).Body.Bytes())
	rec = do(h, "PUT", fmt.Sprintf("/api/projects/%d/accounts/mastodon", p2), fmt.Sprintf(`{"account_id":%d}`, second))
	if rec.Code != 200 {
		t.Fatalf("bind %d %s", rec.Code, rec.Body)
	}
	var views []struct {
		Platform struct{ Name string }
		Account  *struct {
			ID     int64
			Handle string
		}
	}
	for pid, want := range map[int64]string{p1: "first", p2: "second"} {
		rec := do(h, "GET", fmt.Sprintf("/api/projects/%d/accounts", pid), "")
		if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil || len(views) != 4 {
			t.Fatalf("list %d %s", rec.Code, rec.Body)
		}
		for _, v := range views {
			if v.Platform.Name == "mastodon" && (v.Account == nil || v.Account.Handle != want) {
				t.Fatalf("project %d mastodon = %+v, want %s", pid, v.Account, want)
			}
			if v.Platform.Name == "reddit" && v.Account != nil {
				t.Fatal("reddit bound without asking")
			}
		}
		if strings.Contains(rec.Body.String(), "s1") || strings.Contains(rec.Body.String(), "credentials") {
			t.Fatalf("credentials leaked: %s", rec.Body)
		}
	}
	if rec := do(h, "PUT", fmt.Sprintf("/api/projects/%d/accounts/devto", p1), fmt.Sprintf(`{"account_id":%d}`, first)); rec.Code != 422 {
		t.Fatalf("wrong platform %d", rec.Code)
	}
	// B can neither see nor change A's bindings, nor bind A's account.
	for _, c := range []struct{ method, path, body string }{
		{"GET", fmt.Sprintf("/api/projects/%d/accounts", p1), ""},
		{"PUT", fmt.Sprintf("/api/projects/%d/accounts/mastodon", p1), fmt.Sprintf(`{"account_id":%d}`, first)},
		{"DELETE", fmt.Sprintf("/api/projects/%d/accounts/mastodon", p1), ""},
	} {
		if rec := do(other, c.method, c.path, c.body); rec.Code != 404 {
			t.Errorf("B %s %s → %d", c.method, c.path, rec.Code)
		}
	}
	if rec := do(h, "DELETE", fmt.Sprintf("/api/projects/%d/accounts/mastodon", p1), ""); rec.Code != 200 || strings.Contains(rec.Body.String(), `"first"`) {
		t.Fatalf("unbind %d %s", rec.Code, rec.Body)
	}
}
