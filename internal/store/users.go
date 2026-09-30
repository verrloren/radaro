package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// User is a Radaro account. The password hash never leaves the store in JSON.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	IsAdmin      bool   `json:"is_admin"`
	CreatedAt    string `json:"created_at"`
	PasswordHash string `json:"-"`
}

var (
	// ErrRegistrationClosed rejects a sign-up once the first user exists.
	ErrRegistrationClosed = errors.New("registration is closed")
	// ErrTokenReused marks a refresh token presented after it was rotated.
	ErrTokenReused = errors.New("refresh token reuse")
)

// NormalizeEmail trims and lower-cases an address and rejects anything that
// is not a bare address.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", errors.New("enter a valid email address")
	}
	return email, nil
}

const userColumns = `id, email, is_admin, created_at, password_hash`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser adds a user. The first user becomes the admin and may always
// sign up; later ones only when open is true.
func (s *Store) CreateUser(email, passwordHash string, open bool) (*User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var users int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return nil, err
	}
	if users > 0 && !open {
		return nil, ErrRegistrationClosed
	}
	var taken int
	err = tx.QueryRow(`SELECT 1 FROM users WHERE email = ?`, email).Scan(&taken)
	if err == nil {
		return nil, fmt.Errorf("%w: an account with this email already exists", ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := stamp(time.Now())
	res, err := tx.Exec(`INSERT INTO users (email, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		email, passwordHash, users == 0, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if users == 0 {
		// The first user owns what the instance held before accounts existed.
		for _, table := range []string{"projects", "accounts", "drafts", "activity"} {
			if _, err := tx.Exec(`UPDATE `+table+` SET user_id = ? WHERE user_id IS NULL`, id); err != nil {
				return nil, err
			}
		}
	}
	var hasDefault int
	err = tx.QueryRow(`SELECT 1 FROM projects WHERE user_id = ? AND is_default = 1`, id).Scan(&hasDefault)
	if errors.Is(err, sql.ErrNoRows) {
		name := "Default"
		if err := tx.QueryRow(`SELECT 1 FROM projects WHERE user_id = ? AND name = ?`, id, name).Scan(&hasDefault); err == nil {
			name = "Default project"
		}
		_, err = tx.Exec(`INSERT INTO projects (user_id, name, is_default, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`, id, name, now, now)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.restrict()
	return s.User(id)
}

// User returns one user, or nil.
func (s *Store) User(id int64) (*User, error) {
	return scanUser(s.rdb.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// UserByEmail returns the user with this address, or nil.
func (s *Store) UserByEmail(email string) (*User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil, nil
	}
	return scanUser(s.rdb.QueryRow(`SELECT `+userColumns+` FROM users WHERE email = ?`, email))
}

// CountUsers is the number of registered users.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// SetPassword replaces a user's password hash and signs out all sessions.
func (s *Store) SetPassword(id int64, passwordHash string) error {
	now := stamp(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveRefreshToken stores the hash of a new session's refresh token.
func (s *Store) SaveRefreshToken(userID int64, hash []byte, expires time.Time, userAgent string) error {
	_, err := s.db.Exec(`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, created_at, user_agent) VALUES (?, ?, ?, ?, ?)`,
		userID, hash, stamp(expires), stamp(time.Now()), truncate(userAgent, 200))
	return err
}

// RotateRefreshToken swaps a live refresh token for a new one and returns its
// user. A token presented again after it was rotated means it leaked, so every
// session of that user is revoked, unless it comes back within grace: two
// browser tabs refreshing at once is not an attack.
func (s *Store) RotateRefreshToken(oldHash, newHash []byte, expires time.Time, userAgent string, grace time.Duration) (int64, error) {
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var (
		id, userID int64
		expiresAt  string
		revokedAt  sql.NullString
		replacedBy sql.NullInt64
	)
	err = tx.QueryRow(`SELECT id, user_id, expires_at, revoked_at, replaced_by FROM refresh_tokens WHERE token_hash = ?`, oldHash).
		Scan(&id, &userID, &expiresAt, &revokedAt, &replacedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if !parseStamp(expiresAt).After(now) {
		return 0, ErrNotFound
	}
	if revokedAt.Valid && !replacedBy.Valid {
		return 0, ErrNotFound // signed out
	}
	if revokedAt.Valid && now.Sub(parseStamp(revokedAt.String)) > grace {
		if _, err := tx.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, stamp(now), userID); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, ErrTokenReused
	}
	res, err := tx.Exec(`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, created_at, user_agent) VALUES (?, ?, ?, ?, ?)`,
		userID, newHash, stamp(expires), stamp(now), truncate(userAgent, 200))
	if err != nil {
		return 0, err
	}
	newID, _ := res.LastInsertId()
	if !revokedAt.Valid {
		if _, err := tx.Exec(`UPDATE refresh_tokens SET revoked_at = ?, replaced_by = ? WHERE id = ?`, stamp(now), newID, id); err != nil {
			return 0, err
		}
	}
	return userID, tx.Commit()
}

// RevokeRefreshToken ends one session; false when the token is unknown.
func (s *Store) RevokeRefreshToken(hash []byte) (bool, error) {
	res, err := s.db.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, stamp(time.Now()), hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RevokeAllForUser ends every session of a user.
func (s *Store) RevokeAllForUser(userID int64) error {
	_, err := s.db.Exec(`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, stamp(time.Now()), userID)
	return err
}

// PurgeExpiredTokens deletes sessions that expired before t.
func (s *Store) PurgeExpiredTokens(t time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM refresh_tokens WHERE expires_at < ?`, stamp(t))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InstanceSecret returns the named random secret, creating it on first use,
// so a server needs no configuration to sign sessions and keeps them valid
// across restarts.
func (s *Store) InstanceSecret(key string) ([]byte, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO instance (key, value) VALUES (?, ?)`,
		key, base64.StdEncoding.EncodeToString(buf)); err != nil {
		return nil, err
	}
	s.restrict()
	var v string
	if err := s.db.QueryRow(`SELECT value FROM instance WHERE key = ?`, key).Scan(&v); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(v)
}
