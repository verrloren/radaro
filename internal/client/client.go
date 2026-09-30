// Package client is the CLI's HTTP client for a Radaro server. It keeps the
// signed-in session in the user's config directory and refreshes it as
// needed, so every command acts as that user.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotSignedIn means there is no usable session.
var ErrNotSignedIn = errors.New("not signed in: run radaro login --server <url> (or radaro register)")

// Session is what `radaro login` saves.
type Session struct {
	Server           string    `json:"server"`
	Email            string    `json:"email"`
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// ConfigDir is $RADARO_CONFIG_DIR, else the OS config dir + /radaro.
func ConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("RADARO_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "radaro"), nil
}

func sessionPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

// LoadSession reads the saved session; nil when there is none.
func LoadSession() (*Session, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s is damaged; run radaro login again", path)
	}
	return &s, nil
}

// Save writes the session readable by the owner only. The temporary file is
// created 0600, so the tokens are never readable by others, even briefly.
func (s *Session) Save() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".auth-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// NormalizeServer checks a server URL and drops a trailing slash.
func NormalizeServer(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("server must be an http(s) URL, e.g. https://radaro.example.com (got %q)", raw)
	}
	return raw, nil
}

// Insecure reports whether passwords would cross the network in plain text.
func Insecure(server string) bool {
	u, err := url.Parse(server)
	if err != nil || u.Scheme == "https" {
		return false
	}
	h := u.Hostname()
	return h != "localhost" && h != "127.0.0.1" && h != "::1"
}

// Error is a non-2xx answer from the server.
type Error struct {
	Status  int
	Message string
	Body    json.RawMessage // the full response, e.g. a failed publish's draft
}

func (e *Error) Error() string { return e.Message }

// StatusOf is an *Error's HTTP status, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// Client talks to one server as one user.
type Client struct {
	Server  string
	HTTP    *http.Client
	Session *Session // nil for sign-in calls
	Now     func() time.Time
}

// New returns a client for server without a session.
func New(server string) *Client {
	return &Client{Server: server, HTTP: &http.Client{Timeout: 10 * time.Minute}, Now: time.Now}
}

// FromSession returns a client for the saved session.
func FromSession() (*Client, error) {
	s, err := LoadSession()
	if err != nil {
		return nil, err
	}
	if s == nil || s.RefreshToken == "" {
		return nil, ErrNotSignedIn
	}
	c := New(s.Server)
	c.Session = s
	return c, nil
}

// Register creates an account and returns its session (not yet saved).
func (c *Client) Register(ctx context.Context, email, password string) (*Session, error) {
	return c.signIn(ctx, "/api/auth/register", email, password)
}

// Login signs in and returns the session (not yet saved).
func (c *Client) Login(ctx context.Context, email, password string) (*Session, error) {
	return c.signIn(ctx, "/api/auth/login", email, password)
}

type sessionResponse struct {
	User struct {
		Email string `json:"email"`
	} `json:"user"`
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func (r sessionResponse) session(server string) *Session {
	return &Session{Server: server, Email: r.User.Email, AccessToken: r.AccessToken, AccessExpiresAt: r.AccessExpiresAt,
		RefreshToken: r.RefreshToken, RefreshExpiresAt: r.RefreshExpiresAt}
}

func (c *Client) signIn(ctx context.Context, path, email, password string) (*Session, error) {
	var res sessionResponse
	if err := c.send(ctx, http.MethodPost, path, nil, map[string]string{"email": email, "password": password}, &res, ""); err != nil {
		return nil, err
	}
	return res.session(c.Server), nil
}

// Logout ends the session on the server. The caller deletes the saved file.
func (c *Client) Logout(ctx context.Context) error {
	if c.Session == nil {
		return nil
	}
	return c.send(ctx, http.MethodPost, "/api/auth/logout", nil, map[string]string{"refresh_token": c.Session.RefreshToken}, nil, "")
}

// Do calls the API as the signed-in user: body is sent as JSON and the answer
// decoded into out (when not nil). An expired access token is refreshed and
// the call retried once.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if c.Session == nil {
		return ErrNotSignedIn
	}
	if c.Now().Add(30 * time.Second).After(c.Session.AccessExpiresAt) {
		if err := c.refresh(ctx); err != nil {
			return err
		}
	}
	err := c.send(ctx, method, path, query, body, out, c.Session.AccessToken)
	if StatusOf(err) != http.StatusUnauthorized {
		return err
	}
	if err := c.refresh(ctx); err != nil {
		return err
	}
	return c.send(ctx, method, path, query, body, out, c.Session.AccessToken)
}

func (c *Client) refresh(ctx context.Context) error {
	var res sessionResponse
	err := c.send(ctx, http.MethodPost, "/api/auth/refresh", nil, map[string]string{"refresh_token": c.Session.RefreshToken}, &res, "")
	if StatusOf(err) == http.StatusUnauthorized {
		return fmt.Errorf("your session has ended: %w", ErrNotSignedIn)
	}
	if err != nil {
		return err
	}
	next := res.session(c.Server)
	if next.Email == "" {
		next.Email = c.Session.Email
	}
	c.Session = next
	return next.Save()
}

func (c *Client) send(ctx context.Context, method, path string, query url.Values, body, out any, token string) error {
	u := c.Server + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Radaro-Client", "cli")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return &Error{Status: resp.StatusCode, Message: msg, Body: raw}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("unexpected answer from %s%s: %w", c.Server, path, err)
	}
	return nil
}
