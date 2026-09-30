package main

import (
	"net/http"
	"testing"
)

const (
	// Project 2 pools two Reddit accounts and none on Dev.to.
	pools = `[{"platform":{"name":"reddit","label":"Reddit"},"accounts":[` + accLive + `,` + accBlocked + `]},` +
		`{"platform":{"name":"devto","label":"Dev.to"},"accounts":[]}]`
	pathPool       = "/api/projects/2/accounts/reddit"
	errBadProject  = "project id must be a positive integer"
	projectAccount = "accounts"
)

func TestProjectAccountPools(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		"GET /api/projects/2/accounts":         {body: pools},
		"PUT " + pathPool:                      {body: pools},
		"DELETE " + pathPool:                   {body: pools},
		"DELETE " + pathPool + "/2":            {body: pools},
		"PUT /api/projects/2/accounts/myspace": {status: http.StatusUnprocessableEntity, body: `{"error":"unknown platform \"myspace\""}`},
	})
	runCases(t, f, []cliCase{
		{args: []string{"project", projectAccount, "2"}, want: []string{
			"  Reddit    #1 me  (live, 4/5 left)\n", "            #2 me.bsky.social  (limited, paused, 0/3 left)\n",
			"  Dev.to    —  any of your devto accounts (add one: radaro project bind 2 devto <account-id>)",
		}},
		{args: []string{"--json", "project", projectAccount, "2"}, want: []string{`"label": "Reddit"`, `"remaining": 4`}},
		{args: []string{"project", projectAccount, "9"}, err: "not found"},
		{args: []string{"project", projectAccount, "x"}, err: errBadProject},

		{args: []string{"project", "bind", "2", "reddit", "2"}, call: "PUT " + pathPool + ` {"account_id":2}`,
			want: []string{"✓ project 2 publishes on reddit with account 2 (2 in its pool)"}},
		{args: []string{"--json", "project", "bind", "2", "reddit", "2"}, want: []string{`"accounts": [`}},
		{args: []string{"project", "bind", "2", "myspace", "1"}, err: `unknown platform "myspace"`},
		{args: []string{"project", "bind", "2", "reddit", "x"}, err: errBadAccount},
		{args: []string{"project", "bind", "x", "reddit", "1"}, err: errBadProject},

		{args: []string{"project", "unbind", "2", "reddit", "2"}, call: "DELETE " + pathPool + "/2",
			want: []string{"✓ removed account 2 from project 2's reddit pool (2 left)"}},
		{args: []string{"project", "unbind", "2", "reddit"}, call: "DELETE " + pathPool,
			want: []string{"✓ emptied project 2's reddit pool"}},
		{args: []string{"--json", "project", "unbind", "2", "reddit"}, want: []string{`"name": "devto"`}},
		{args: []string{"project", "unbind", "2", "devto"}, err: "not found"},
		{args: []string{"project", "unbind", "2", "reddit", "0"}, err: errBadAccount},
		{args: []string{"project", "unbind", "x", "reddit"}, err: errBadProject},
	})
	if poolSize(nil, "reddit") != 0 {
		t.Fatal("poolSize of nothing")
	}
}

func TestConnectAddsToPool(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{"POST /api/accounts": {body: accBare}})
	out, err := run(t, "api-key\n", "connect", "devto", "--project", "2")
	if err != nil || !contains(out, "✓ connected devto as dev (account 3)", "added to project 2's devto pool") {
		t.Fatalf("connect %v: %s", err, out)
	}
	if !f.called(`POST /api/accounts {"platform":"devto","project_id":2,"secret":"api-key"}`) {
		t.Fatalf("calls %q", f.calls)
	}
}
