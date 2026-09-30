package main

import (
	"bufio"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
)

func init() { auth.FastHashingForTests() }

// run executes the CLI with args, feeding input on stdin, and returns stdout.
func run(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	stdin = bufio.NewReader(strings.NewReader(input))
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	root := newRoot()
	root.SetArgs(args)
	runErr := root.Execute()
	w.Close()
	os.Stdout = saved
	return <-done, runErr
}

func TestCLIWorksThroughTheServer(t *testing.T) {
	t.Setenv("RADARO_CONFIG_DIR", t.TempDir())
	t.Setenv("RADARO_SERVER", "")
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := server.New(&config.Config{Sources: []string{"hackernews"}, AccessTTL: time.Minute, RefreshTTL: time.Hour}, st, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	if _, err := run(t, "", "project", "list"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("before login: %v", err)
	}
	out, err := run(t, "correct horse\n", "register", "--server", ts.URL, "--email", "me@example.com", "--password-stdin")
	if err != nil || !strings.Contains(out, "signed in as me@example.com") {
		t.Fatalf("register: %q, %v", out, err)
	}
	if out, err := run(t, "", "whoami"); err != nil || !strings.Contains(out, "me@example.com (admin)") {
		t.Fatalf("whoami: %q, %v", out, err)
	}
	if out, err := run(t, "", "project", "create", "Launch"); err != nil || !strings.Contains(out, "id 2") {
		t.Fatalf("create: %q, %v", out, err)
	}
	if out, err := run(t, "", "project", "add", "2", "radaro", "social listening", "Radaro"); err != nil || !strings.Contains(out, "added 2 keyword(s)") {
		t.Fatalf("add: %q, %v", out, err)
	}
	if out, err := run(t, "", "project", "keywords", "2"); err != nil || !strings.Contains(out, "social listening") {
		t.Fatalf("keywords: %q, %v", out, err)
	}
	if out, err := run(t, "", "project", "remove", "2", "RADARO"); err != nil || !strings.Contains(out, `removed "radaro"`) {
		t.Fatalf("remove: %q, %v", out, err)
	}
	if out, err := run(t, "", "project", "accounts", "2"); err != nil || !strings.Contains(out, "radaro project bind 2 reddit") {
		t.Fatalf("accounts: %q, %v", out, err)
	}
	if out, err := run(t, "", "--json", "project", "list"); err != nil || !strings.Contains(out, `"name": "Launch"`) {
		t.Fatalf("list: %q, %v", out, err)
	}
	out, err = run(t, "", "draft", "add", "--platform", "devto", "--project", "2", "--title", "Hello", "--body", "First post")
	if err != nil || !strings.Contains(out, "draft 1 created") {
		t.Fatalf("draft add: %q, %v", out, err)
	}
	if _, err := run(t, "", "publish", "1"); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("publish unapproved: %v", err)
	}
	if out, err := run(t, "", "draft", "approve", "1"); err != nil || !strings.Contains(out, "approved") {
		t.Fatalf("approve: %q, %v", out, err)
	}
	if _, err := run(t, "", "publish", "1"); err == nil || !strings.Contains(err.Error(), "no devto account") {
		t.Fatalf("publish without an account: %v", err)
	}
	if out, err := run(t, "", "activity"); err != nil || !strings.Contains(out, "draft.approved") {
		t.Fatalf("activity: %q, %v", out, err)
	}
	if out, err := run(t, "", "status"); err != nil || !strings.Contains(out, "user       me@example.com") {
		t.Fatalf("status: %q, %v", out, err)
	}
	if out, err := run(t, "", "logout"); err != nil || !strings.Contains(out, "signed out") {
		t.Fatalf("logout: %q, %v", out, err)
	}
	if _, err := run(t, "", "project", "list"); err == nil {
		t.Fatal("commands still work after logout")
	}
	// The server is remembered; a wrong password is refused.
	if _, err := run(t, "wrong password\n", "login", "--email", "me@example.com", "--password-stdin"); err == nil ||
		!strings.Contains(err.Error(), "invalid email or password") {
		t.Fatalf("wrong password: %v", err)
	}
	if out, err := run(t, "correct horse\n", "login", "--email", "me@example.com", "--password-stdin"); err != nil || !strings.Contains(out, ts.URL) {
		t.Fatalf("login: %q, %v", out, err)
	}
}

func TestAdminResetPassword(t *testing.T) {
	db := t.TempDir() + "/radaro.db"
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser("me@example.com", "old-hash", false)
	st.SaveRefreshToken(u.ID, []byte("t"), time.Now().Add(time.Hour), "")
	st.Close()
	if out, err := run(t, "new password 1\n", "--db", db, "admin", "reset-password", "--email", "ME@example.com", "--password-stdin"); err != nil ||
		!strings.Contains(out, "new password set") {
		t.Fatalf("reset: %q, %v", out, err)
	}
	st, _ = store.Open(db)
	defer st.Close()
	got, _ := st.User(u.ID)
	if !auth.CheckPassword(got.PasswordHash, "new password 1") {
		t.Fatal("the password did not change")
	}
	if _, err := st.RotateRefreshToken([]byte("t"), []byte("t2"), time.Now().Add(time.Hour), "", time.Minute); err == nil {
		t.Fatal("old sessions survived the reset")
	}
	if out, err := run(t, "", "--db", db, "admin", "users"); err != nil || !strings.Contains(out, "me@example.com (admin)") {
		t.Fatalf("users: %q, %v", out, err)
	}
}
