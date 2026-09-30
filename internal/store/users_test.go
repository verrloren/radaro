package store

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestCreateUserFirstIsAdminThenRegistrationRules(t *testing.T) {
	st := openTest(t)
	first, err := st.CreateUser("  Alice@Example.com ", "h1", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Email != "alice@example.com" || !first.IsAdmin {
		t.Fatalf("first user = %+v, want normalized admin", first)
	}
	if _, err := st.CreateUser("bob@example.com", "h2", false); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration err = %v", err)
	}
	bob, err := st.CreateUser("bob@example.com", "h2", true)
	if err != nil || bob.IsAdmin {
		t.Fatalf("second user = %+v, %v; want a non-admin", bob, err)
	}
	if _, err := st.CreateUser("ALICE@example.com", "h3", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate email err = %v", err)
	}
	for _, bad := range []string{"", "alice", "Alice <a@b.c>", "a@b.c, d@e.f"} {
		if _, err := st.CreateUser(bad, "h", true); err == nil {
			t.Fatalf("accepted email %q", bad)
		}
	}
	got, err := st.UserByEmail("BOB@example.com")
	if err != nil || got == nil || got.ID != bob.ID || got.PasswordHash != "h2" {
		t.Fatalf("UserByEmail = %+v, %v", got, err)
	}
	if n, _ := st.CountUsers(); n != 2 {
		t.Fatalf("CountUsers = %d", n)
	}
}

func TestRefreshTokenRotationAndReuse(t *testing.T) {
	st := openTest(t)
	u, err := st.CreateUser("a@example.com", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	other := []byte("other-session")
	if err := st.SaveRefreshToken(u.ID, []byte("t1"), later, "cli"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRefreshToken(u.ID, other, later, "web"); err != nil {
		t.Fatal(err)
	}
	id, err := st.RotateRefreshToken([]byte("t1"), []byte("t2"), later, "cli", 0)
	if err != nil || id != u.ID {
		t.Fatalf("rotate = %d, %v", id, err)
	}
	// t1 again, past the grace period: treated as stolen, everything is revoked.
	if _, err := st.RotateRefreshToken([]byte("t1"), []byte("t3"), later, "cli", 0); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("reuse err = %v", err)
	}
	for _, h := range [][]byte{[]byte("t2"), other} {
		if _, err := st.RotateRefreshToken(h, []byte("x"+string(h)), later, "", 0); err == nil {
			t.Fatalf("session %q survived a reuse", h)
		}
	}
	if _, err := st.RotateRefreshToken([]byte("nope"), []byte("t4"), later, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token err = %v", err)
	}
}

func TestRefreshTokenGraceAndExpiry(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("a@example.com", "h", false)
	later := time.Now().Add(time.Hour)
	st.SaveRefreshToken(u.ID, []byte("t1"), later, "")
	if _, err := st.RotateRefreshToken([]byte("t1"), []byte("t2"), later, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	// A second tab refreshing with t1 at the same moment gets its own session.
	if _, err := st.RotateRefreshToken([]byte("t1"), []byte("t2b"), later, "", time.Minute); err != nil {
		t.Fatalf("rotation inside grace failed: %v", err)
	}
	if _, err := st.RotateRefreshToken([]byte("t2"), []byte("t3"), later, "", time.Minute); err != nil {
		t.Fatalf("the first tab's session was revoked: %v", err)
	}

	st.SaveRefreshToken(u.ID, []byte("old"), time.Now().Add(-time.Second), "")
	if _, err := st.RotateRefreshToken([]byte("old"), []byte("new"), later, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token err = %v", err)
	}
	if n, err := st.PurgeExpiredTokens(time.Now()); err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
}

func TestSetPasswordRevokesSessions(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("a@example.com", "h", false)
	st.SaveRefreshToken(u.ID, []byte("t1"), time.Now().Add(time.Hour), "")
	if err := st.SetPassword(u.ID, "h2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RotateRefreshToken([]byte("t1"), []byte("t2"), time.Now().Add(time.Hour), "", time.Minute); err == nil {
		t.Fatal("a session survived a password change")
	}
	if err := st.SetPassword(999, "h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user err = %v", err)
	}
}

func TestInstanceSecretIsStable(t *testing.T) {
	st := openTest(t)
	a, err := st.InstanceSecret("jwt")
	if err != nil || len(a) != 48 {
		t.Fatalf("secret = %d bytes, %v", len(a), err)
	}
	b, _ := st.InstanceSecret("jwt")
	if !bytes.Equal(a, b) {
		t.Fatal("secret changed between calls")
	}
}
