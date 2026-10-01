package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/verrloren/radaro/internal/netproxy"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
)

type redditBrowser interface {
	Login(context.Context, string, string) error
	Screenshot(context.Context) (redditbrowser.Screen, error)
	Input(context.Context, redditbrowser.Input) error
	Finish(context.Context) (*redditbrowser.Credentials, error)
	Close()
}
type pendingBrowser struct {
	mu                sync.Mutex
	userID, projectID int64
	expires           time.Time
	browser           redditBrowser
	cancel            context.CancelFunc
}
type browserLogins struct {
	sync.Mutex
	sessions map[string]*pendingBrowser
}

func (s *Server) redditBrowserAvailable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"available": redditbrowser.Executable() != ""})
}

func (s *Server) startRedditBrowser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Proxy     string `json:"proxy_url"`
		ProjectID int64  `json:"project_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Proxy = strings.TrimSpace(body.Proxy)
	if body.Username == "" || body.Password == "" || len(body.Username) > 320 || len(body.Password) > 512 {
		writeError(w, 422, "Reddit login and password are required")
		return
	}
	if err := netproxy.Validate(body.Proxy); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if body.ProjectID != 0 && !s.ownProject(w, r, body.ProjectID) {
		return
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		internalError(w, err)
		return
	}
	id := hex.EncodeToString(buf)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	p := &pendingBrowser{userID: userID(r), projectID: body.ProjectID, expires: time.Now().Add(10 * time.Minute), cancel: cancel}
	s.browserLogins.Lock()
	if s.browserLogins.sessions == nil {
		s.browserLogins.sessions = make(map[string]*pendingBrowser)
	}
	count := 0
	for _, v := range s.browserLogins.sessions {
		if v.userID == p.userID {
			count++
		}
	}
	if count >= 2 {
		s.browserLogins.Unlock()
		cancel()
		writeError(w, 429, "finish or cancel an existing Reddit sign-in first")
		return
	}
	s.browserLogins.sessions[id] = p
	s.browserLogins.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	setupCtx, setupCancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer setupCancel()
	stop := context.AfterFunc(setupCtx, cancel)
	defer stop()
	b, err := s.openRedditBrowser(ctx, body.Proxy, true)
	if err != nil {
		s.removeBrowser(id, p)
		writeError(w, 503, err.Error())
		return
	}
	p.browser = b
	context.AfterFunc(ctx, func() { p.mu.Lock(); defer p.mu.Unlock(); s.removeBrowser(id, p) })
	if err = b.Login(setupCtx, body.Username, body.Password); err != nil {
		s.removeBrowser(id, p)
		writeError(w, 502, err.Error())
		return
	}
	// Do not retain the password in the pending session or account credentials.
	body.Password = ""
	screen, err := b.Screenshot(setupCtx)
	if err != nil {
		s.removeBrowser(id, p)
		writeError(w, 502, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"session_id": id, "expires_at": p.expires.UTC().Format(time.RFC3339), "screen": screen})
}

func (s *Server) removeBrowser(id string, p *pendingBrowser) {
	s.browserLogins.Lock()
	if s.browserLogins.sessions[id] == p {
		delete(s.browserLogins.sessions, id)
	}
	s.browserLogins.Unlock()
	p.cancel()
	if p.browser != nil {
		p.browser.Close()
		p.browser = nil
	}
}

func (s *Server) withBrowser(w http.ResponseWriter, r *http.Request, fn func(string, *pendingBrowser)) {
	id := chi.URLParam(r, "session")
	s.browserLogins.Lock()
	p := s.browserLogins.sessions[id]
	s.browserLogins.Unlock()
	if p == nil || p.userID != userID(r) {
		writeError(w, 404, "Reddit sign-in not found")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.browser == nil || time.Now().After(p.expires) {
		s.removeBrowser(id, p)
		writeError(w, 404, "Reddit sign-in expired; start again")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	fn(id, p)
}

func (s *Server) redditBrowserInput(w http.ResponseWriter, r *http.Request) {
	var in redditbrowser.Input
	if !decode(w, r, &in) {
		return
	}
	s.withBrowser(w, r, func(_ string, p *pendingBrowser) {
		if in.Kind != "refresh" {
			if err := p.browser.Input(r.Context(), in); err != nil {
				writeError(w, 422, err.Error())
				return
			}
		}
		screen, err := p.browser.Screenshot(r.Context())
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, screen)
	})
}

func (s *Server) finishRedditBrowser(w http.ResponseWriter, r *http.Request) {
	s.withBrowser(w, r, func(id string, p *pendingBrowser) {
		if p.projectID != 0 && !s.ownProject(w, r, p.projectID) {
			return
		}
		creds, err := p.browser.Finish(r.Context())
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		if creds == nil || creds.Username == "" || len(creds.Cookies) == 0 {
			writeError(w, 409, "Reddit has not confirmed the sign-in yet")
			return
		}
		acc, err := s.store.SaveAccount(p.userID, "reddit", creds.Username, publish.RedditCredentials{Username: creds.Username, Browser: creds})
		if err != nil {
			internalError(w, err)
			return
		}
		if p.projectID != 0 {
			if _, err = s.store.BindAccount(p.userID, p.projectID, acc.ID); err != nil {
				storeError(w, err, 500)
				return
			}
		}
		_, err = s.store.SetAccountStatus(acc.ID, "live", "Browser sign-in verified", time.Time{})
		if err != nil {
			internalError(w, err)
			return
		}
		acc, err = s.store.Account(p.userID, acc.ID)
		if err != nil {
			internalError(w, err)
			return
		}
		_ = s.store.LogActivity(p.userID, "account.connected", 0, "reddit "+creds.Username+" through browser")
		s.removeBrowser(id, p)
		writeJSON(w, 201, acc)
	})
}

func (s *Server) cancelRedditBrowser(w http.ResponseWriter, r *http.Request) {
	s.withBrowser(w, r, func(id string, p *pendingBrowser) {
		s.removeBrowser(id, p)
		writeJSON(w, 200, map[string]bool{"cancelled": true})
	})
}

func (s *Server) updateRedditBrowserProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Proxy string `json:"proxy_url"`
	}
	if !decode(w, r, &body) {
		return
	}
	acc, ok := s.accountFromPath(w, r)
	if !ok {
		return
	}
	var creds publish.RedditCredentials
	if acc.Platform != "reddit" || json.Unmarshal(acc.Credentials, &creds) != nil || creds.Browser == nil {
		writeError(w, 422, "this account does not use a Reddit browser session")
		return
	}
	body.Proxy = strings.TrimSpace(body.Proxy)
	if err := netproxy.Validate(body.Proxy); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	creds.Browser.Proxy = body.Proxy
	if err := s.store.UpdateAccountCredentials(acc.ID, creds); err != nil {
		internalError(w, err)
		return
	}
	_ = s.store.LogActivity(userID(r), "account.proxy_changed", 0, "reddit "+acc.Handle)
	writeJSON(w, 200, map[string]bool{"configured": body.Proxy != ""})
}
