package client

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
)

func init() { auth.FastHashingForTests() }

func newServer(t *testing.T) string {
	t.Helper()
	t.Setenv("RADARO_CONFIG_DIR", t.TempDir())
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv, err := server.New(&config.Config{Sources: []string{"hackernews"}, AccessTTL: time.Minute, RefreshTTL: time.Hour}, st, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestSessionLifecycle(t *testing.T) {
	url := newServer(t)
	ctx := context.Background()
	if _, err := FromSession(); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("no session err = %v", err)
	}
	sess, err := New(url).Register(ctx, "me@example.com", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Email != "me@example.com" || sess.RefreshToken == "" {
		t.Fatalf("session = %+v", sess)
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	dir, _ := ConfigDir()
	info, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %v, %v", info.Mode().Perm(), err)
	}

	c, err := FromSession()
	if err != nil {
		t.Fatal(err)
	}
	var me struct{ Email string }
	if err := c.Do(ctx, "GET", "/api/auth/me", nil, nil, &me); err != nil || me.Email != "me@example.com" {
		t.Fatalf("me = %+v, %v", me, err)
	}
	// An expired access token is refreshed before the call, and saved.
	c.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	old := c.Session.RefreshToken
	var projects []map[string]any
	if err := c.Do(ctx, "GET", "/api/projects", nil, nil, &projects); err != nil || len(projects) != 1 {
		t.Fatalf("projects after expiry = %v, %v", projects, err)
	}
	saved, _ := LoadSession()
	if saved.RefreshToken == old || saved.RefreshToken != c.Session.RefreshToken {
		t.Fatal("the rotated session was not saved")
	}
	// A rejected access token is refreshed and the call retried.
	c.Now = time.Now
	c.Session.AccessToken = "garbage"
	if err := c.Do(ctx, "GET", "/api/auth/me", nil, nil, &me); err != nil {
		t.Fatalf("retry after 401: %v", err)
	}
	// Errors carry the server's message and status.
	err = c.Do(ctx, "POST", "/api/projects", nil, map[string]string{"name": ""}, nil)
	if StatusOf(err) != 422 || err.Error() != "project name must not be empty" {
		t.Fatalf("error = %v (%d)", err, StatusOf(err))
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	c.Session.AccessToken = "garbage"
	if err := c.Do(ctx, "GET", "/api/auth/me", nil, nil, nil); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("after logout err = %v", err)
	}
}

func TestLoginErrors(t *testing.T) {
	url := newServer(t)
	ctx := context.Background()
	if _, err := New(url).Login(ctx, "me@example.com", "correct horse"); StatusOf(err) != 401 {
		t.Fatalf("unknown user err = %v", err)
	}
	if _, err := New("http://127.0.0.1:1").Login(ctx, "a@b.c", "x"); err == nil {
		t.Fatal("no error for an unreachable server")
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"https://radaro.example.com/": "https://radaro.example.com",
		" http://127.0.0.1:8042 ":     "http://127.0.0.1:8042",
	} {
		if got, err := NormalizeServer(in); err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "radaro.example.com", "ftp://x"} {
		if _, err := NormalizeServer(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if !Insecure("http://radaro.example.com") || Insecure("https://radaro.example.com") || Insecure("http://127.0.0.1:8042") {
		t.Fatal("Insecure is wrong")
	}
}
