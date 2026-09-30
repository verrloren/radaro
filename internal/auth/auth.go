// Package auth signs users in: bcrypt passwords, short-lived HS256 access
// tokens and rotating refresh tokens stored as hashes.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/store"
)

const issuer = "radaro"

// refreshGrace lets two tabs refresh with the same token at once without
// being treated as a stolen token.
const refreshGrace = 30 * time.Second

// bcryptCost is a variable so tests can hash quickly.
var bcryptCost = 12

// FastHashingForTests lowers the bcrypt cost; tests of other packages call it.
func FastHashingForTests() { bcryptCost = bcrypt.MinCost }

var (
	// ErrInvalidCredentials is the one answer to a bad email or password, so
	// sign-in does not reveal which addresses exist.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrUnauthenticated rejects a missing, expired or forged token.
	ErrUnauthenticated = errors.New("authentication required")
)

// Session is what a successful sign-in returns.
type Session struct {
	User             *store.User `json:"user"`
	AccessToken      string      `json:"access_token"`
	AccessExpiresAt  time.Time   `json:"access_expires_at"`
	RefreshToken     string      `json:"refresh_token"`
	RefreshExpiresAt time.Time   `json:"refresh_expires_at"`
}

// Service issues and checks sessions.
type Service struct {
	store      *store.Store
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	open       bool
	grace      time.Duration
	now        func() time.Time
}

// New builds the service. Without RADARO_JWT_SECRET it signs with a random
// secret kept in the database, so sessions survive restarts.
func New(st *store.Store, cfg *config.Config) (*Service, error) {
	secret := []byte(cfg.JWTSecret)
	if len(secret) == 0 {
		var err error
		if secret, err = st.InstanceSecret("jwt_secret"); err != nil {
			return nil, err
		}
	}
	return &Service{
		store: st, secret: secret, accessTTL: cfg.AccessTTL, refreshTTL: cfg.RefreshTTL,
		open: cfg.Registration == "open", grace: refreshGrace, now: time.Now,
	}, nil
}

// RegistrationOpen reports whether anyone may sign up. The first user always can.
func (a *Service) RegistrationOpen() (bool, error) {
	if a.open {
		return true, nil
	}
	n, err := a.store.CountUsers()
	return n == 0, err
}

// Register creates a user and signs them in.
func (a *Service) Register(email, password, userAgent string) (*Session, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u, err := a.store.CreateUser(email, hash, a.open)
	if err != nil {
		return nil, err
	}
	return a.issue(u, userAgent)
}

// Login checks a password and starts a session.
func (a *Service) Login(email, password, userAgent string) (*Session, error) {
	u, err := a.store.UserByEmail(email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		// Spend the same time as a real check so timing does not reveal
		// whether the address exists.
		CheckPassword(dummyHash(), password)
		return nil, ErrInvalidCredentials
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return a.issue(u, userAgent)
}

// Refresh trades a refresh token for a new session.
func (a *Service) Refresh(refreshToken, userAgent string) (*Session, error) {
	if refreshToken == "" {
		return nil, ErrUnauthenticated
	}
	plain, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	expires := a.now().Add(a.refreshTTL)
	userID, err := a.store.RotateRefreshToken(hashToken(refreshToken), hash, expires, userAgent, a.grace)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrTokenReused) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	u, err := a.store.User(userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUnauthenticated
	}
	access, accessExp, err := a.signAccess(u.ID)
	if err != nil {
		return nil, err
	}
	return &Session{User: u, AccessToken: access, AccessExpiresAt: accessExp, RefreshToken: plain, RefreshExpiresAt: expires}, nil
}

// Logout ends the session behind a refresh token. Unknown tokens are ignored.
func (a *Service) Logout(refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	_, err := a.store.RevokeRefreshToken(hashToken(refreshToken))
	return err
}

// Verify checks an access token and returns its user id.
func (a *Service) Verify(accessToken string) (int64, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(accessToken, &claims, func(*jwt.Token) (any, error) { return a.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(a.now),
	)
	if err != nil {
		return 0, ErrUnauthenticated
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || id < 1 {
		return 0, ErrUnauthenticated
	}
	return id, nil
}

func (a *Service) issue(u *store.User, userAgent string) (*Session, error) {
	access, accessExp, err := a.signAccess(u.ID)
	if err != nil {
		return nil, err
	}
	plain, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	refreshExp := a.now().Add(a.refreshTTL)
	if err := a.store.SaveRefreshToken(u.ID, hash, refreshExp, userAgent); err != nil {
		return nil, err
	}
	return &Session{User: u, AccessToken: access, AccessExpiresAt: accessExp, RefreshToken: plain, RefreshExpiresAt: refreshExp}, nil
}

func (a *Service) signAccess(userID int64) (string, time.Time, error) {
	now := a.now()
	exp := now.Add(a.accessTTL)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}).SignedString(a.secret)
	return tok, exp, err
}

// ValidatePassword enforces the length rules; bcrypt ignores bytes past 72.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

// HashPassword validates and hashes a password.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// CheckPassword compares in constant time.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

var (
	dummyOnce sync.Once
	dummy     string
)

func dummyHash() string {
	dummyOnce.Do(func() {
		h, _ := bcrypt.GenerateFromPassword([]byte("radaro-dummy-password"), bcryptCost)
		dummy = string(h)
	})
	return dummy
}

func newRefreshToken() (string, []byte, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	plain := base64.RawURLEncoding.EncodeToString(buf)
	return plain, hashToken(plain), nil
}

func hashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}
