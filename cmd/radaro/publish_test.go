package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// draftJSON is an approved Bluesky post; plan is the server's publishing plan.
func draftJSON(id int64, status, plan string) string {
	return `{"id":` + strconv.FormatInt(id, 10) + `,"platform":"bluesky","account_id":null,"kind":"post","body":"hello world",` +
		`"status":"` + status + `","remote_url":null,"plan":` + plan + `}`
}

const (
	planNow     = `{"account":` + accLive + `,"quota":null,"next_at":null,"reason":""}`
	planBlocked = `{"account":null,"quota":null,"next_at":"` + nextAtRaw + `","reason":"minimum interval"}`
	published   = `{"id":1,"platform":"bluesky","status":"published","body":"hello world","remote_url":"https://bsky.app/p/1"}`
	pathPublish = "POST /api/drafts/1/publish"
	errLimit    = "minimum interval between publications"
)

// publishRefusal is what `publish --json` prints when nothing went out.
type publishRefusal struct {
	Draft struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"draft"`
	Account json.RawMessage `json:"account"`
	Error   string          `json:"error"`
	NextAt  *time.Time      `json:"next_at"`
}

func refusal(t *testing.T, args ...string) publishRefusal {
	t.Helper()
	out, err := run(t, "", append([]string{"--json", "publish"}, args...)...)
	checkErr(t, err, "exit 1")
	var r publishRefusal
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return r
}

func publishFake(t *testing.T) *fakeAPI {
	return newFakeAPI(t, map[string]fakeRoute{
		"GET /api/drafts/1": {body: draftJSON(1, "approved", planNow)},
		pathPublish:         {body: `{"draft":` + published + `,"account":` + accLive + `}`},
		"GET /api/drafts/2": {body: draftJSON(2, "approved", planBlocked)},
		"POST /api/drafts/2/publish": {status: http.StatusConflict,
			body: `{"error":"` + errLimit + `","next_at":"` + nextAtRaw + `"}`},
		"GET /api/drafts/3": {body: draftJSON(3, "approved", "null")},
		"POST /api/drafts/3/publish": {status: http.StatusBadGateway,
			body: `{"error":"publishing failed: account suspended","draft":` + draftJSON(3, "failed", "null") + `}`},
		"GET /api/drafts/4":          {body: draftJSON(4, "draft", planNow)},
		"GET /api/drafts/5":          {body: draftJSON(5, "approved", "null")},
		"POST /api/drafts/5/publish": {status: http.StatusUnprocessableEntity, body: `{"error":"no usable bluesky account"}`},
		"GET /api/drafts/6":          {body: draftJSON(6, "approved", "null")},
		"POST /api/drafts/6/publish": {body: `{"id":6,"status":"published","remote_url":"https://bsky.app/p/6"}`},
		"GET /api/drafts/7":          {body: draftJSON(7, "approved", "null")},
		"POST /api/drafts/7/publish": {status: http.StatusInternalServerError, body: "boom"},
		"GET /api/drafts/8":          {body: draftJSON(8, "approved", "null")},
		"POST /api/drafts/8/publish": {body: `{"draft":"oops"}`},
	})
}

func TestPublishText(t *testing.T) {
	f := publishFake(t)
	runCases(t, f, []cliCase{
		{args: []string{"publish", "1"}, want: []string{"✓ published draft 1 as me: https://bsky.app/p/1"}, call: pathPublish},
		{args: []string{"--json", "publish", "1"}, want: []string{`"remote_url": "https://bsky.app/p/1"`}},
		{args: []string{"publish", "2"}, err: errLimit + "; it may go out after " + nextAtLocal(t) + " (see radaro accounts)"},
		{args: []string{"publish", "3"}, err: "publishing failed: account suspended"},
		{args: []string{"publish", "4"}, err: "not approved"},
		{args: []string{"publish", "5"}, err: "no usable bluesky account"},
		{args: []string{"publish", "6"}, want: []string{"✓ published draft 6: https://bsky.app/p/6"}},
		{args: []string{"publish", "7"}, err: "boom"},
		{args: []string{"publish", "8"}, err: "unexpected answer to publish"},
		{args: []string{"publish", "9"}, err: "draft 9 does not exist"},
		{args: []string{"publish", "x"}, err: "draft id must be a positive number"},
	})
}

func TestPublishJSONFailures(t *testing.T) {
	publishFake(t)
	r := refusal(t, "2")
	if r.Draft.ID != 2 || r.Draft.Status != "approved" || r.Error != errLimit || r.NextAt == nil ||
		!r.NextAt.Equal(time.Date(2030, 1, 2, 15, 4, 0, 0, time.UTC)) || string(r.Account) != "null" {
		t.Fatalf("limit refusal %+v", r)
	}
	if r := refusal(t, "3"); r.Draft.Status != "failed" || r.NextAt != nil || r.Error == "" {
		t.Fatalf("platform failure %+v", r)
	}
	if r := refusal(t, "7"); r.Draft.ID != 7 || r.Error != "boom" {
		t.Fatalf("server error %+v", r)
	}
}

func TestDraftShowPlan(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		"GET /api/drafts/1": {body: draftJSON(1, "approved", planNow)},
		"GET /api/drafts/2": {body: `{"id":2,"platform":"bluesky","account_id":1,"kind":"post","body":"x","status":"approved","plan":` + planBlocked + `}`},
		"GET /api/drafts/3": {body: draftJSON(3, "published", "null")},
	})
	runCases(t, f, []cliCase{
		{args: []string{"draft", "show", "1"}, want: []string{"Account:   #1 me (picked at publish time)\n", "Next:      now\n"}},
		{args: []string{"draft", "show", "2"}, want: []string{"Account:   none usable\n", "Next:      " + nextAtLocal(t) + " — minimum interval\n"}},
		{args: []string{"--json", "draft", "show", "1"}, want: []string{`"plan": {`, `"remaining": 4`}},
		{args: []string{"draft", "show", "4"}, err: "draft 4 does not exist"},
	})
	out, err := run(t, "", "draft", "show", "3")
	if err != nil || contains(out, "Account:") || !contains(out, "hello world") {
		t.Fatalf("published draft %v: %s", err, out)
	}
}

func TestPlanNext(t *testing.T) {
	at := time.Date(2030, 1, 2, 15, 4, 0, 0, time.UTC)
	for want, p := range map[string]*draftPlan{
		"now":                      {},
		when(at):                   {NextAt: &at},
		"blocked — account paused": {Reason: "account paused"},
		when(at) + " — cooldown":   {NextAt: &at, Reason: "cooldown"},
	} {
		if got := planNext(p); got != want {
			t.Errorf("planNext(%+v) = %q, want %q", p, got, want)
		}
	}
}

func TestDraftEditAccount(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		"GET /api/drafts/1":   {body: draftJSON(1, "approved", planNow)},
		"PATCH /api/drafts/1": {body: draftJSON(1, "draft", planNow)},
	})
	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCases(t, f, []cliCase{
		{args: []string{"draft", "edit", "1", "--account", "3"}, call: `PATCH /api/drafts/1 {"account_id":3}`,
			want: []string{"✓ draft 1 updated"}},
		{args: []string{"draft", "edit", "1", "--account", "0"}, call: `PATCH /api/drafts/1 {"account_id":null}`},
		{args: []string{"draft", "edit", "1", "--title", "T", "--community", "c", "--body-file", bodyFile},
			call: `PATCH /api/drafts/1 {"body":"from a file","community":"c","title":"T"}`},
		{args: []string{"--json", "draft", "edit", "1", "--body", "b"}, call: `PATCH /api/drafts/1 {"body":"b"}`, want: []string{`"plan": {`}},
		{args: []string{"draft", "edit", "1", "--body-file", bodyFile + ".missing"}, err: "no such file"},
	})
}

func TestStatsMarksRemovedPosts(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{"GET /api/stats": {body: `{"published":[{"id":1,"platform":"reddit",` +
		`"remote_url":"https://reddit.com/r/x/1","removed_at":"2030-01-01T00:00:00Z","body":"hello","metrics":{"score":3}}],"errors":[]}`}})
	runCases(t, f, []cliCase{{args: []string{"stats"}, want: []string{"https://reddit.com/r/x/1  [removed]", "score 3"}}})
}
