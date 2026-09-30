package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	nextAtRaw = "2030-01-02T15:04:00Z"
	// A live Reddit account on the platform defaults.
	accLive = `{"id":1,"platform":"reddit","handle":"me","status":"live","status_detail":null,"paused":false,` +
		`"limits":{"daily":5,"min_interval_sec":600,"community_cooldown_h":24,"custom":false},` +
		`"quota":{"daily":5,"used_24h":1,"remaining":4,"next_at":null,"ready":true},` +
		`"activity":{"published_24h":1}}`
	// A paused, rate-limited Bluesky account with its own limits.
	accBlocked = `{"id":2,"platform":"bluesky","handle":"me.bsky.social","status":"limited",` +
		`"status_detail":"paused automatically: a post was removed","paused":true,` +
		`"limits":{"daily":3,"min_interval_sec":900,"community_cooldown_h":0,"custom":true},` +
		`"quota":{"daily":3,"used_24h":3,"remaining":0,"next_at":"` + nextAtRaw + `","reason":"daily limit reached","ready":false}}`
	// An account whose quota the server could not compute.
	accBare = `{"id":3,"platform":"devto","handle":"dev","status":"unknown","paused":false}`

	accountsList   = `{"platforms":[],"accounts":[` + accLive + `,` + accBlocked + `,` + accBare + `]}`
	errNotFound    = "does not exist"
	errBadAccount  = "account id must be a positive number"
	pathAccounts   = "GET /api/accounts"
	pathAccount1   = "PATCH /api/accounts/1"
	pathCheckAll   = "POST /api/accounts/check"
	defaultsLimits = "5 per 24h, 10m0s apart, 24h per community (platform defaults)"
)

func nextAtLocal(t *testing.T) string {
	t.Helper()
	at, err := time.Parse(time.RFC3339, nextAtRaw)
	if err != nil {
		t.Fatal(err)
	}
	return when(at)
}

func TestAccountsList(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{pathAccounts: {body: accountsList}})
	runCases(t, f, []cliCase{
		{args: []string{"accounts"}, want: []string{
			"ID  PLATFORM  HANDLE", "4/5    now", "limited*", "0/3", nextAtLocal(t) + " — daily limit reached",
			"paused automatically: a post was removed", "unknown    —      —", "* paused: resume with radaro accounts resume <id>",
		}},
		{args: []string{"--json", "accounts"}, want: []string{`"remaining": 4`, `"next_at": null`, `"published_24h": 1`}},
	})
	out, err := run(t, "", "--json", "accounts")
	var list []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &list) != nil || len(list) != 3 {
		t.Fatalf("json list %v: %s", err, out)
	}
}

func TestAccountsListEmptyAndBroken(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{pathAccounts: {body: `{"platforms":[],"accounts":null}`}})
	runCases(t, f, []cliCase{
		{args: []string{"accounts"}, want: []string{noAccountsHint}},
		{args: []string{"--json", "accounts"}, want: []string{"[]"}},
	})
	f.set(pathAccounts, fakeRoute{body: `{"accounts":{"id":1}}`})
	runCases(t, f, []cliCase{{args: []string{"accounts"}, err: "unexpected list of accounts"}})
	f.set(pathAccounts, fakeRoute{status: http.StatusInternalServerError, body: `{"error":"database is locked"}`})
	runCases(t, f, []cliCase{
		{args: []string{"accounts"}, err: "database is locked"},
		{args: []string{"accounts", "limits", "1"}, err: "database is locked"},
	})
}

func TestAccountsCheck(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		pathAccounts:                 {body: accountsList},
		pathCheckAll:                 {body: `{"platforms":[],"accounts":[` + accLive + `],"error":"account 2: bluesky: timeout"}`},
		"POST /api/accounts/1/check": {body: accLive},
		"POST /api/accounts/2/check": {status: http.StatusBadGateway, body: `{"error":"bluesky: timeout"}`},
		"POST /api/accounts/5/check": {status: http.StatusBadGateway, body: `{"error":"gone"}`},
	})
	runCases(t, f, []cliCase{
		{args: []string{"accounts", "check"}, want: []string{"  ! account 2: bluesky: timeout", "4/5"}, call: pathCheckAll},
		{args: []string{"--json", "accounts", "check"}, want: []string{`"errors": [`, `"account 2: bluesky: timeout"`, `"remaining": 4`}},
		{args: []string{"accounts", "check", "1"}, want: []string{"   1  reddit    me"}, call: "POST /api/accounts/1/check"},
		{args: []string{"--json", "accounts", "check", "1"}, want: []string{`"errors": []`, `"id": 1`}},
		{args: []string{"accounts", "check", "2"}, err: "exit 1",
			want: []string{"account 2: bluesky: timeout (status left unchanged)", "me.bsky.social"}},
		{args: []string{"--json", "accounts", "check", "2"}, err: "exit 1", want: []string{`"id": 2`, "status left unchanged"}},
		{args: []string{"accounts", "check", "5"}, err: "account 5 " + errNotFound},
		{args: []string{"accounts", "check", "9"}, err: "account 9 " + errNotFound},
		{args: []string{"accounts", "check", "x"}, err: errBadAccount},
	})
	f.set(pathCheckAll, fakeRoute{body: `{"accounts":[],"errors":["account 1: reddit: 500"]}`})
	runCases(t, f, []cliCase{{args: []string{"accounts", "check"}, want: []string{"! account 1: reddit: 500", noAccountsHint}}})
	f.set(pathCheckAll, fakeRoute{body: `{"accounts":"broken"}`})
	runCases(t, f, []cliCase{{args: []string{"accounts", "check"}, err: "unexpected list of accounts"}})
	f.set(pathCheckAll, fakeRoute{status: http.StatusForbidden, body: `{"error":"forbidden"}`})
	runCases(t, f, []cliCase{{args: []string{"accounts", "check"}, err: "forbidden"}})
}

func TestAccountsPauseResume(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{pathAccount1: {body: accLive}})
	runCases(t, f, []cliCase{
		{args: []string{"accounts", "pause", "1"}, call: pathAccount1 + ` {"paused":true}`,
			want: []string{"✓ account 1 (reddit me) paused; it will not publish until resumed", "limits: " + defaultsLimits}},
		{args: []string{"accounts", "resume", "1"}, call: pathAccount1 + ` {"paused":false}`, want: []string{"(reddit me) resumed"}},
		{args: []string{"--json", "accounts", "resume", "1"}, want: []string{`"limits": {`, `"published_24h": 1`}},
		{args: []string{"accounts", "pause", "9"}, err: "account 9 " + errNotFound},
		{args: []string{"accounts", "resume", "0"}, err: errBadAccount},
	})
}

func TestAccountsLimits(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{
		pathAccounts:            {body: accountsList},
		pathAccount1:            {body: accLive},
		"PATCH /api/accounts/2": {status: http.StatusUnprocessableEntity, body: `{"error":"daily_limit must be between 0 and 1000"}`},
	})
	runCases(t, f, []cliCase{
		{args: []string{"accounts", "limits", "1", "--daily", "3", "--interval", "15m", "--cooldown", "48h"},
			call: pathAccount1 + ` {"daily_limit":3,"min_interval_sec":900,"community_cooldown_h":48}`, want: []string{"limits updated"}},
		{args: []string{"accounts", "limits", "1", "--daily", "0"}, call: pathAccount1 + ` {"daily_limit":0}`},
		{args: []string{"accounts", "limits", "1", "--interval", "0", "--cooldown", "0"},
			call: pathAccount1 + ` {"min_interval_sec":0,"community_cooldown_h":0}`},
		{args: []string{"accounts", "limits", "1"}, want: []string{"✓ account 1 (reddit me) limits\n", defaultsLimits}},
		{args: []string{"accounts", "limits", "2"}, want: []string{"limits: 3 per 24h, 15m0s apart\n"}},
		{args: []string{"accounts", "limits", "3"}, want: []string{"limits: unknown"}},
		{args: []string{"--json", "accounts", "limits", "2"}, want: []string{`"custom": true`}},
		{args: []string{"accounts", "limits", "7"}, err: "account 7 " + errNotFound},
		{args: []string{"accounts", "limits", "x"}, err: errBadAccount},
		{args: []string{"accounts", "limits", "x", "--daily", "1"}, err: errBadAccount},
		{args: []string{"accounts", "limits", "2", "--daily", "5000"}, err: "between 0 and 1000"},
		{args: []string{"accounts", "limits", "1", "--daily", "-1"}, err: "--daily"},
		{args: []string{"accounts", "limits", "1", "--interval", "500ms"}, err: "--interval"},
		{args: []string{"accounts", "limits", "1", "--interval", "-1m"}, err: "--interval"},
		{args: []string{"accounts", "limits", "1", "--cooldown", "90m"}, err: "--cooldown"},
		{args: []string{"accounts", "limits", "1", "--cooldown", "-24h"}, err: "--cooldown"},
	})
}

func TestAccountsRemove(t *testing.T) {
	f := newFakeAPI(t, map[string]fakeRoute{"DELETE /api/accounts/1": {status: http.StatusNoContent}})
	runCases(t, f, []cliCase{
		{args: []string{"accounts", "remove", "1"}, want: []string{"✓ removed account 1"}, call: "DELETE /api/accounts/1"},
		{args: []string{"accounts", "remove", "2"}, err: "account 2 " + errNotFound},
		{args: []string{"accounts", "remove", "x"}, err: errBadAccount},
	})
}

func TestNextText(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, nextAtRaw)
	for want, q := range map[string]*quotaView{
		"—":                        nil,
		"now":                      {Ready: true},
		"blocked":                  {},
		"blocked — account paused": {Reason: "account paused"},
		when(at):                   {NextAt: &at},
	} {
		if got := nextText(q); got != want {
			t.Errorf("nextText(%+v) = %q, want %q", q, got, want)
		}
	}
	if !strings.Contains(limitsLine(&limitsView{Daily: 2, MinIntervalSec: 3600, Custom: true}), "2 per 24h, 1h0m0s apart") {
		t.Error("limitsLine")
	}
}
