package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
)

type fakePublisher struct {
	fail    bool
	posted  []publish.Post
	metrics publish.Metrics
}

func (f *fakePublisher) Publish(_ context.Context, p publish.Post) (publish.Result, error) {
	if f.fail {
		return publish.Result{}, errors.New("HTTP 500")
	}
	f.posted = append(f.posted, p)
	return publish.Result{RemoteID: "r1", URL: "https://example.test/r1"}, nil
}

func (f *fakePublisher) Metrics(context.Context, string) (publish.Metrics, error) { return f.metrics, nil }

func TestDraftLifecycleOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	fake := &fakePublisher{metrics: publish.Metrics{"likes": 7}}
	var usedCreds string
	srv.newPublisher = func(platform string, creds json.RawMessage, _ string) (publish.Publisher, error) {
		usedCreds = string(creds)
		return fake, nil
	}
	srv.connect = func(_ context.Context, platform string, in publish.ConnectInput) (string, any, error) {
		return in.Handle, map[string]string{"token": in.Secret}, nil
	}
	h := signedIn(t, srv, "a@example.com")
	other := signedIn(t, srv, "b@example.com")

	// An opportunity: the seeded mention has a URL and no draft.
	rec := do(h, "GET", "/api/opportunities", "")
	var opps []struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &opps); err != nil || len(opps) != 1 {
		t.Fatalf("opportunities %d %s", rec.Code, rec.Body)
	}
	if rec := do(other, "GET", "/api/opportunities", ""); rec.Body.String() != "[]\n" {
		t.Fatalf("B sees A's opportunities: %s", rec.Body)
	}
	do(h, "POST", "/api/accounts", `{"platform":"mastodon","handle":"me","secret":"tok"}`)

	body := fmt.Sprintf(`{"platform":"mastodon","body":"Have you tried Radaro?","mention_id":%q}`, opps[0].ID)
	rec = do(h, "POST", "/api/drafts", body)
	var d struct {
		ID        int64
		Status    string
		Query     *string
		ProjectID *int64 `json:"project_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); rec.Code != 201 || err != nil || d.Query == nil || *d.Query != "go" || d.ProjectID == nil {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/opportunities", ""); rec.Body.String() != "[]\n" {
		t.Fatalf("a drafted mention is still an opportunity: %s", rec.Body)
	}
	if rec := do(other, "POST", "/api/drafts", body); rec.Code != 404 {
		t.Fatalf("B drafted on A's mention: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/drafts", `{"platform":"mastodon","body":"`+strings.Repeat("x", 501)+`"}`); rec.Code != 422 {
		t.Fatalf("over-long draft %d", rec.Code)
	}

	path := fmt.Sprintf("/api/drafts/%d", d.ID)
	if rec := do(h, "POST", path+"/publish", ""); rec.Code != 409 {
		t.Fatalf("publish before approval %d", rec.Code)
	}
	for _, c := range []struct{ method, suffix string }{{"GET", ""}, {"PATCH", ""}, {"POST", "/approve"}, {"POST", "/skip"}, {"POST", "/publish"}} {
		if rec := do(other, c.method, path+c.suffix, `{}`); rec.Code != 404 {
			t.Errorf("B %s %s → %d", c.method, path+c.suffix, rec.Code)
		}
	}
	if rec := do(h, "PATCH", path, `{"body":"Edited text"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Edited text") {
		t.Fatalf("edit %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", path+"/approve", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"approved"`) {
		t.Fatalf("approve %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", path+"/publish", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"published"`) || len(fake.posted) != 1 || fake.posted[0].Body != "Edited text" {
		t.Fatalf("publish %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(usedCreds, "tok") {
		t.Fatalf("published with the wrong credentials: %s", usedCreds)
	}
	if rec := do(h, "POST", path+"/publish", ""); rec.Code != 409 || len(fake.posted) != 1 {
		t.Fatalf("published twice: %d", rec.Code)
	}
	rec = do(h, "GET", "/api/stats?refresh=true", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"likes":7`) {
		t.Fatalf("stats %d %s", rec.Code, rec.Body)
	}
	if rec := do(other, "GET", "/api/stats", ""); strings.Contains(rec.Body.String(), "Edited") {
		t.Fatal("B sees A's published drafts")
	}
	rec = do(h, "GET", "/api/activity", "")
	for _, action := range []string{"draft.created", "draft.approved", "draft.published"} {
		if !strings.Contains(rec.Body.String(), action) {
			t.Errorf("activity lacks %s: %s", action, rec.Body)
		}
	}
	if rec := do(other, "GET", "/api/activity", ""); strings.Contains(rec.Body.String(), "draft.") {
		t.Fatalf("B reads A's activity: %s", rec.Body)
	}
}

func TestFailedPublishIsReported(t *testing.T) {
	srv, st := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	srv.newPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) {
		return &fakePublisher{fail: true}, nil
	}
	h := signedIn(t, srv, "a@example.com")
	acc, _ := st.SaveAccount(1, "devto", "me", map[string]string{"api_key": "k"})
	_ = acc
	rec := do(h, "POST", "/api/drafts", `{"platform":"devto","title":"T","body":"B"}`)
	var d struct{ ID int64 }
	json.Unmarshal(rec.Body.Bytes(), &d)
	do(h, "POST", fmt.Sprintf("/api/drafts/%d/approve", d.ID), "")
	rec = do(h, "POST", fmt.Sprintf("/api/drafts/%d/publish", d.ID), "")
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), `"failed"`) || !strings.Contains(rec.Body.String(), "HTTP 500") {
		t.Fatalf("failed publish %d %s", rec.Code, rec.Body)
	}
}

func TestReportExportStatus(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(h, "GET", "/api/report?q=go", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("report %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/export", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"great"`) {
		t.Fatalf("export %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "GET", "/api/status", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"owner@example.com"`) || !strings.Contains(rec.Body.String(), `"database"`) {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Fatal("status leaks the password hash")
	}
}
