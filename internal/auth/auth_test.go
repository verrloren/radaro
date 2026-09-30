package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/store"
)

func init() { bcryptCost = bcrypt.MinCost }

func newService(t *testing.T, registration string) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, err := New(st, &config.Config{AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, Registration: registration})
	if err != nil {
		t.Fatal(err)
	}
	return a, st
}

func TestRegisterLoginVerify(t *testing.T) {
	a, _ := newService(t, "closed")
	if open, _ := a.RegistrationOpen(); !open {
		t.Fatal("the first user must be able to sign up")
	}
	s, err := a.Register("Owner@Example.com", "correct horse", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !s.User.IsAdmin || s.RefreshToken == "" {
		t.Fatalf("session = %+v", s)
	}
	id, err := a.Verify(s.AccessToken)
	if err != nil || id != s.User.ID {
		t.Fatalf("verify = %d, %v", id, err)
	}
	if open, _ := a.RegistrationOpen(); open {
		t.Fatal("registration stayed open after the first user")
	}
	if _, err := a.Register("second@example.com", "correct horse", ""); !errors.Is(err, store.ErrRegistrationClosed) {
		t.Fatalf("closed registration err = %v", err)
	}
	if _, err := a.Login("owner@example.com", "correct horse", ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	for _, c := range [][2]string{{"owner@example.com", "wrong password"}, {"nobody@example.com", "correct horse"}} {
		if _, err := a.Login(c[0], c[1], ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login %v err = %v", c, err)
		}
	}
}

func TestPasswordRules(t *testing.T) {
	a, _ := newService(t, "open")
	for _, pw := range []string{"short", strings.Repeat("x", 73)} {
		if _, err := a.Register("a@example.com", pw, ""); err == nil {
			t.Fatalf("accepted password of %d bytes", len(pw))
		}
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	a, _ := newService(t, "open")
	s, err := a.Register("a@example.com", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	sign := func(method jwt.SigningMethod, key any, c jwt.RegisteredClaims) string {
		tok, err := jwt.NewWithClaims(method, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	now := time.Now()
	valid := jwt.RegisteredClaims{Issuer: issuer, Subject: "1", ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}
	expired := valid
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Hour))
	noExp := valid
	noExp.ExpiresAt = nil
	otherIssuer := valid
	otherIssuer.Issuer = "someone"
	for name, tok := range map[string]string{
		"empty":        "",
		"garbage":      "not.a.token",
		"other secret": sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-00"), valid),
		"alg none":     sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, valid),
		"HS512":        sign(jwt.SigningMethodHS512, a.secret, valid),
		"expired":      sign(jwt.SigningMethodHS256, a.secret, expired),
		"no exp":       sign(jwt.SigningMethodHS256, a.secret, noExp),
		"issuer":       sign(jwt.SigningMethodHS256, a.secret, otherIssuer),
		"tampered":     s.AccessToken[:len(s.AccessToken)-2] + "xx",
	} {
		if _, err := a.Verify(tok); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := a.Verify(sign(jwt.SigningMethodHS256, a.secret, valid)); err != nil {
		t.Fatalf("a well-formed token failed: %v", err)
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	a, _ := newService(t, "open")
	s, err := a.Register("a@example.com", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.Refresh(s.RefreshToken, "")
	if err != nil || next.RefreshToken == s.RefreshToken || next.User.ID != s.User.ID {
		t.Fatalf("refresh = %+v, %v", next, err)
	}
	// Replay the first token after the grace period.
	a.grace = 0
	if _, err := a.Refresh(s.RefreshToken, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("reuse err = %v", err)
	}
	if _, err := a.Refresh(next.RefreshToken, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("the rotated session survived a detected reuse")
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	a, _ := newService(t, "open")
	s, _ := a.Register("a@example.com", "correct horse", "")
	if err := a.Logout(s.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(s.RefreshToken, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("refresh after logout err = %v", err)
	}
	if err := a.Logout("unknown"); err != nil {
		t.Fatal(err)
	}
}

func TestSecretFromConfigAndDatabase(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	cfg := &config.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour}
	a1, _ := New(st, cfg)
	a2, _ := New(st, cfg)
	if string(a1.secret) != string(a2.secret) || len(a1.secret) < 32 {
		t.Fatal("the stored secret is not reused")
	}
	cfg.JWTSecret = strings.Repeat("s", 40)
	a3, _ := New(st, cfg)
	if string(a3.secret) != cfg.JWTSecret {
		t.Fatal("RADARO_JWT_SECRET was ignored")
	}
}
