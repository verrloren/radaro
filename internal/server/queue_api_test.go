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

type healthPublisher struct {
	fakePublisher
	checkErr error
}

func (p *healthPublisher) Check(context.Context) (publish.Health, error) {
	if p.checkErr != nil {
		return publish.Health{}, p.checkErr
	}
	return publish.Health{Status: publish.HealthLive}, nil
}

func responseID(t *testing.T, body []byte) int64 {
	t.Helper()
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.ID == 0 {
		t.Fatalf("missing id in %s: %v", body, err)
	}
	return v.ID
}

func TestAccountHealthLimitsAndStatsOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	srv.connect = func(_ context.Context, _ string, in publish.ConnectInput) (string, any, error) {
		return in.Handle, map[string]string{"token": in.Secret}, nil
	}
	srv.newPublisher = func(_ string, creds json.RawMessage, _ string) (publish.Publisher, error) {
		var c struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, err
		}
		if c.Token == "offline" {
			return &healthPublisher{checkErr: errors.New("temporary outage")}, nil
		}
		return &healthPublisher{}, nil
	}
	a := signedIn(t, srv, "a@example.com")
	b := signedIn(t, srv, "b@example.com")
	good := do(a, "POST", "/api/accounts", `{"platform":"mastodon","handle":"good","secret":"private-token"}`)
	bad := do(a, "POST", "/api/accounts", `{"platform":"mastodon","handle":"offline","secret":"offline"}`)
	if good.Code != 201 || bad.Code != 201 {
		t.Fatalf("connect: %d %s; %d %s", good.Code, good.Body, bad.Code, bad.Body)
	}
	goodID, badID := responseID(t, good.Body.Bytes()), responseID(t, bad.Body.Bytes())
	path := fmt.Sprintf("/api/accounts/%d", goodID)
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", path, `{"daily_limit":2}`},
		{"POST", path + "/check", ""},
	} {
		if rec := do(b, tc.method, tc.path, tc.body); rec.Code != 404 {
			t.Errorf("foreign %s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	for body, want := range map[string]int{`{}`: 422, `{"daily_limit":-1}`: 422, `{"daily_limit":1001}`: 422} {
		if rec := do(a, "PATCH", path, body); rec.Code != want {
			t.Errorf("limits %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(a, "PATCH", path, `{"daily_limit":2,"min_interval_sec":0,"paused":true}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"daily_limit":2`) || !strings.Contains(rec.Body.String(), `"paused":true`) {
		t.Fatalf("update %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "private-token") {
		t.Fatalf("account secret leaked: %s", rec.Body)
	}
	rec = do(a, "POST", path+"/check", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"live"`) {
		t.Fatalf("check %d %s", rec.Code, rec.Body)
	}
	rec = do(a, "POST", "/api/accounts/check", "")
	var checkResult struct {
		Error  string   `json:"error"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &checkResult); rec.Code != 200 || err != nil ||
		!strings.Contains(checkResult.Error, "temporary outage") || len(checkResult.Errors) != 1 ||
		!strings.HasPrefix(checkResult.Errors[0], fmt.Sprintf("account %d: ", badID)) ||
		!strings.Contains(checkResult.Errors[0], "temporary outage") {
		t.Fatalf("partial check %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/accounts", "/api/accounts/stats?days=1"} {
		rec = do(b, "GET", path, "")
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "good") || strings.Contains(rec.Body.String(), "offline") {
			t.Fatalf("foreign summary %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	rec = do(a, "GET", "/api/accounts/stats?days=1", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":2`) || !strings.Contains(rec.Body.String(), `"live":1`) {
		t.Fatalf("stats %d %s", rec.Code, rec.Body)
	}
	if rec := do(a, "GET", "/api/accounts/stats?days=91", ""); rec.Code != 422 {
		t.Fatalf("invalid days: %d %s", rec.Code, rec.Body)
	}
}

func TestProjectAccountPoolRemovesOneAndKeepsOther(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	srv.connect = func(_ context.Context, _ string, in publish.ConnectInput) (string, any, error) {
		return in.Handle, map[string]string{"token": in.Secret}, nil
	}
	a := signedIn(t, srv, "a@example.com")
	b := signedIn(t, srv, "b@example.com")
	pid := responseID(t, do(a, "POST", "/api/projects", `{"name":"Pool"}`).Body.Bytes())
	a1 := responseID(t, do(a, "POST", "/api/accounts", `{"platform":"mastodon","handle":"one","secret":"s1"}`).Body.Bytes())
	a2 := responseID(t, do(a, "POST", "/api/accounts", `{"platform":"mastodon","handle":"two","secret":"s2"}`).Body.Bytes())
	base := fmt.Sprintf("/api/projects/%d/accounts/mastodon", pid)
	for _, aid := range []int64{a1, a2} {
		if rec := do(a, "PUT", base, fmt.Sprintf(`{"account_id":%d}`, aid)); rec.Code != 200 {
			t.Fatalf("bind %d: %d %s", aid, rec.Code, rec.Body)
		}
	}
	if rec := do(b, "PUT", base, fmt.Sprintf(`{"account_id":%d}`, a1)); rec.Code != 404 {
		t.Fatalf("foreign project bind: %d %s", rec.Code, rec.Body)
	}
	if rec := do(b, "DELETE", fmt.Sprintf("%s/%d", base, a1), ""); rec.Code != 404 {
		t.Fatalf("foreign project unbind: %d %s", rec.Code, rec.Body)
	}
	rec := do(a, "DELETE", fmt.Sprintf("%s/%d", base, a1), "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"handle":"one"`) || !strings.Contains(rec.Body.String(), `"handle":"two"`) {
		t.Fatalf("remove one account: %d %s", rec.Code, rec.Body)
	}
	if rec := do(a, "DELETE", fmt.Sprintf("%s/%d", base, a1), ""); rec.Code != 404 {
		t.Fatalf("remove twice: %d %s", rec.Code, rec.Body)
	}
}

func TestDraftPlanCountsWarningsAndPublishLimitOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}, Registration: "open"})
	fake := &fakePublisher{}
	srv.newPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) { return fake, nil }
	srv.connect = func(_ context.Context, _ string, in publish.ConnectInput) (string, any, error) {
		return in.Handle, map[string]string{"token": in.Secret}, nil
	}
	a := signedIn(t, srv, "a@example.com")
	b := signedIn(t, srv, "b@example.com")
	acc := do(a, "POST", "/api/accounts", `{"platform":"mastodon","handle":"me","secret":"private-token"}`)
	if acc.Code != 201 {
		t.Fatalf("connect %d %s", acc.Code, acc.Body)
	}
	aid := responseID(t, acc.Body.Bytes())
	if rec := do(a, "PATCH", fmt.Sprintf("/api/accounts/%d", aid), `{"daily_limit":1,"min_interval_sec":0}`); rec.Code != 200 {
		t.Fatalf("limits %d %s", rec.Code, rec.Body)
	}
	create := func(body string) int64 {
		t.Helper()
		rec := do(a, "POST", "/api/drafts", fmt.Sprintf(`{"platform":"mastodon","body":%q}`, body))
		if rec.Code != 201 {
			t.Fatalf("create draft %d %s", rec.Code, rec.Body)
		}
		return responseID(t, rec.Body.Bytes())
	}
	first, second := create("First post"), create("Second post")
	firstPath := fmt.Sprintf("/api/drafts/%d", first)
	for _, suffix := range []string{"", "/warnings"} {
		if rec := do(b, "GET", firstPath+suffix, ""); rec.Code != 404 {
			t.Fatalf("foreign draft %s: %d %s", suffix, rec.Code, rec.Body)
		}
	}
	rec := do(a, "GET", firstPath, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"id":%d`, aid)) || !strings.Contains(rec.Body.String(), `"plan"`) {
		t.Fatalf("plan %d %s", rec.Code, rec.Body)
	}
	rec = do(a, "GET", firstPath+"/warnings", "")
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("warnings %d %s", rec.Code, rec.Body)
	}
	if rec := do(b, "GET", "/api/drafts/counts", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), `"draft":2`) {
		t.Fatalf("foreign counts: %d %s", rec.Code, rec.Body)
	}
	rec = do(a, "GET", "/api/drafts/counts", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"draft":2`) || !strings.Contains(rec.Body.String(), `"published":0`) {
		t.Fatalf("counts %d %s", rec.Code, rec.Body)
	}
	for _, id := range []int64{first, second} {
		if rec := do(a, "POST", fmt.Sprintf("/api/drafts/%d/approve", id), ""); rec.Code != 200 {
			t.Fatalf("approve %d: %d %s", id, rec.Code, rec.Body)
		}
	}
	if rec := do(a, "POST", firstPath+"/publish", ""); rec.Code != 200 {
		t.Fatalf("first publish %d %s", rec.Code, rec.Body)
	}
	rec = do(a, "POST", fmt.Sprintf("/api/drafts/%d/publish", second), "")
	if rec.Code != 409 || len(fake.posted) != 1 || !strings.Contains(rec.Body.String(), `"next_at"`) {
		t.Fatalf("daily limit %d %s; posts=%d", rec.Code, rec.Body, len(fake.posted))
	}
	rec = do(a, "GET", fmt.Sprintf("/api/drafts/%d", second), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"approved"`) || !strings.Contains(rec.Body.String(), `"reason"`) {
		t.Fatalf("blocked plan %d %s", rec.Code, rec.Body)
	}
	if rec := do(a, "GET", "/api/drafts/counts", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"published":1`) || !strings.Contains(rec.Body.String(), `"approved":1`) {
		t.Fatalf("final counts %d %s", rec.Code, rec.Body)
	}
}
