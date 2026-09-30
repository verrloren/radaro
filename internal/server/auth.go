package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/store"
)

const (
	accessCookie  = "radaro_access"
	refreshCookie = "radaro_refresh"
	// cliHeader asks for tokens in the response body instead of cookies.
	cliHeader = "X-Radaro-Client"
)

type ctxKey struct{}

// userID is the signed-in user of a request that passed requireAuth.
func userID(r *http.Request) int64 {
	id, _ := r.Context().Value(ctxKey{}).(int64)
	return id
}

// requireAuth accepts a Bearer token (CLI) or the access cookie (dashboard).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if h := r.Header.Get("Authorization"); h != "" {
			if t, ok := strings.CutPrefix(h, "Bearer "); ok {
				token = strings.TrimSpace(t)
			}
		} else if c, err := r.Cookie(accessCookie); err == nil {
			token = c.Value
		}
		id, err := s.auth.Verify(token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="radaro"`)
			writeError(w, http.StatusUnauthorized, auth.ErrUnauthenticated.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// requireAdmin lets only the instance admin change instance-wide settings.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.store.User(userID(r))
		if err != nil {
			internalError(w, err)
			return
		}
		if u == nil || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "only the admin can change instance settings")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authConfig(w http.ResponseWriter, _ *http.Request) {
	open, err := s.auth.RegistrationOpen()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registration_open": open})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if !decode(w, r, &body) {
		return
	}
	sess, err := s.auth.Register(body.Email, body.Password, r.UserAgent())
	if err != nil {
		authError(w, err)
		return
	}
	s.writeSession(w, r, http.StatusCreated, sess)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if !decode(w, r, &body) {
		return
	}
	sess, err := s.auth.Login(body.Email, body.Password, r.UserAgent())
	if err != nil {
		authError(w, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	sess, err := s.auth.Refresh(refreshToken(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			s.clearCookies(w, r)
		}
		authError(w, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(refreshToken(r)); err != nil {
		internalError(w, err)
		return
	}
	s.clearCookies(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.User(userID(r))
	if err != nil {
		internalError(w, err)
		return
	}
	if u == nil {
		writeError(w, http.StatusUnauthorized, auth.ErrUnauthenticated.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// refreshToken comes from the JSON body (CLI) or the refresh cookie.
func refreshToken(r *http.Request) string {
	if r.Header.Get(cliHeader) != "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		return body.RefreshToken
	}
	if c, err := r.Cookie(refreshCookie); err == nil {
		return c.Value
	}
	return ""
}

// writeSession hands the CLI its tokens and the browser httpOnly cookies, so
// page scripts never see a token.
func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, status int, sess *auth.Session) {
	if r.Header.Get(cliHeader) != "" {
		writeJSON(w, status, sess)
		return
	}
	secure := s.secureCookies(r)
	http.SetCookie(w, &http.Cookie{
		Name: accessCookie, Value: sess.AccessToken, Path: "/", Expires: sess.AccessExpiresAt,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookie, Value: sess.RefreshToken, Path: "/api/auth", Expires: sess.RefreshExpiresAt,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, status, map[string]any{"user": sess.User, "access_expires_at": sess.AccessExpiresAt})
}

func (s *Server) clearCookies(w http.ResponseWriter, r *http.Request) {
	secure := s.secureCookies(r)
	for name, path := range map[string]string{accessCookie: "/", refreshCookie: "/api/auth"} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: path, MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	}
}

func (s *Server) secureCookies(r *http.Request) bool {
	return s.cfg.CookieSecure || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthenticated):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, store.ErrRegistrationClosed):
		writeError(w, http.StatusForbidden, "registration is closed on this server")
	default:
		storeError(w, err, http.StatusUnprocessableEntity)
	}
}

// --- rate limiting --------------------------------------------------------------

// limiter is a per-client token bucket for the sign-in endpoints: enough for
// people, too slow for password guessing.
type limiter struct {
	mu      sync.Mutex
	perMin  float64
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMin, burst int) *limiter {
	return &limiter{perMin: float64(perMin), burst: float64(burst), buckets: map[string]*bucket{}, now: time.Now}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.Sub(b.last) > time.Hour {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Minutes()*l.perMin)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "too many attempts, try again in a minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the peer address, or behind a local reverse proxy the address
// the proxy appended last to X-Forwarded-For (earlier entries are
// client-controlled).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	return host
}
