package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectImportReadsFileAndReportsEachRow(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		"POST /api/accounts/import": {body: `{"results":[{"row":1,"platform":"bluesky","handle":"alice","status":"added"},{"row":2,"platform":"mastodon","handle":"bob","status":"updated"}],"added":1,"updated":1,"errors":0}`},
	})
	path := filepath.Join(t.TempDir(), "accounts.txt")
	input := "bluesky|alice|super-secret\nmastodon|bob|other-secret\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "", "connect", "import", path)
	if err != nil || !contains(out, "added row 1: bluesky alice", "updated row 2: mastodon bob", "1 added, 1 updated, 0 errors") {
		t.Fatalf("output %q, error %v", out, err)
	}
	if strings.Contains(out, "super-secret") || strings.Contains(out, "other-secret") {
		t.Fatalf("secrets printed: %s", out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatalf("calls: %q", f.calls)
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(f.calls[0], "POST /api/accounts/import ")), &body); err != nil || body.Text != input {
		t.Fatalf("request %q, decode error %v", f.calls[0], err)
	}
}

func TestConnectImportStdinJSONAndPartialError(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		"POST /api/accounts/import": {body: `{"results":[{"row":1,"platform":"devto","handle":"writer","status":"added","account_id":7},{"row":2,"status":"error","error":"row 2: unsupported platform"}],"added":1,"updated":0,"errors":1}`},
	})
	input := "devto|writer|secret\nunknown|x|private\n"
	out, err := run(t, input, "--json", "connect", "import", "-")
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !contains(out, `"added": 1`, `"errors": 1`, `"account_id": 7`, `"row 2: unsupported platform"`) {
		t.Fatalf("output %q, error %v", out, err)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "private") {
		t.Fatalf("secrets printed: %s", out)
	}
	plain, err := run(t, input, "connect", "import", "-")
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !contains(plain, "added row 1: devto writer", "! row 2: unsupported platform", "1 added, 0 updated, 1 errors") || strings.Contains(plain, "row 2: row 2:") {
		t.Fatalf("text output %q, error %v", plain, err)
	}
	if !f.called(`POST /api/accounts/import {"text":"devto|writer|secret\nunknown|x|private\n"}`) {
		t.Fatalf("stdin was not sent to API: %q", f.calls)
	}
}

func TestConnectImportRejectsEmptyInputAndProjectFlag(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{})
	_, err := run(t, " \n", "connect", "import", "-")
	checkErr(t, err, "account list is empty")
	_, err = run(t, "x", "connect", "--project", "2", "import", "-")
	checkErr(t, err, "--project is not supported")
	if len(f.calls) != 0 {
		t.Fatalf("invalid imports called API: %q", f.calls)
	}
}
