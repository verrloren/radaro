package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/client"
)

// fakeRoute is a canned answer; status 0 means 200.
type fakeRoute struct {
	status int
	body   string
}

// fakeAPI is a Radaro server that answers "METHOD /path" with canned JSON and
// records every request, so commands are tested without a database.
type fakeAPI struct {
	routes map[string]fakeRoute
	mu     sync.Mutex
	calls  []string // "METHOD /path body"
}

// newFakeAPI serves routes and signs the CLI in to it.
func newFakeAPI(t *testing.T, routes map[string]fakeRoute) *fakeAPI {
	t.Helper()
	f := &fakeAPI{routes: routes}
	ts := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(ts.Close)
	t.Setenv("RADARO_CONFIG_DIR", t.TempDir())
	t.Setenv("RADARO_SERVER", "")
	s := &client.Session{Server: ts.URL, Email: "me@example.com", AccessToken: "access", AccessExpiresAt: time.Now().Add(time.Hour),
		RefreshToken: "refresh", RefreshExpiresAt: time.Now().Add(time.Hour)}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.calls = append(f.calls, strings.TrimSpace(key+" "+string(b)))
	rt, ok := f.routes[key]
	f.mu.Unlock()
	if !ok {
		rt = fakeRoute{http.StatusNotFound, `{"error":"not found"}`}
	}
	if rt.status == 0 {
		rt.status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rt.status)
	_, _ = io.WriteString(w, rt.body)
}

// set changes one canned answer.
func (f *fakeAPI) set(key string, rt fakeRoute) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = rt
}

func (f *fakeAPI) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, call)
}

// cliCase runs radaro with args and checks what it printed and returned.
type cliCase struct {
	args []string
	want []string // substrings of stdout
	err  string   // substring of the error; empty = must succeed
	call string   // a request the server must have received
}

func runCases(t *testing.T, f *fakeAPI, cases []cliCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := run(t, "", tc.args...)
			checkErr(t, err, tc.err)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if tc.call != "" && !f.called(tc.call) {
				t.Errorf("server did not get %q; got %q", tc.call, f.calls)
			}
		})
	}
}

func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Fatalf("error %v, want %q", err, want)
	}
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
