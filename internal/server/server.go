// Package server serves the embedded dashboard and the local JSON API
// described in docs/API.md.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/llm"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/netproxy"
	"github.com/verrloren/radaro/internal/outbox"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/sources"
	"github.com/verrloren/radaro/internal/store"
)

// Server holds what the handlers need.
type Server struct {
	cfg               *config.Config
	store             *store.Store
	version           string
	assets            fs.FS // built dashboard (web/dist)
	oauth             oauthStates
	auth              *auth.Service
	limiter           *limiter
	outbox            *outbox.Service
	browserLogins     browserLogins
	replyRequests     replyRequests
	llmConnection     llmConnection
	newLLM            func() (llm.Provider, error)
	loadRedditPost    func(context.Context, redditbrowser.Credentials, string) (redditbrowser.PostDetails, error)
	openRedditBrowser func(context.Context, string, bool) (redditBrowser, error)

	// connect and redditExchange reach the platforms; tests replace them.
	connect        func(ctx context.Context, platform string, in publish.ConnectInput) (string, any, error)
	redditExchange func(ctx context.Context, r *publish.Reddit, code string) error
	newPublisher   func(platform string, credentials json.RawMessage, version string) (publish.Publisher, error)
}

// New returns a server over an open store. assets may be nil (API only).
func New(cfg *config.Config, st *store.Store, version string, assets fs.FS) (*Server, error) {
	proxyURL, err := st.ProxyURL()
	if err != nil {
		return nil, err
	}
	if err := netproxy.Set(proxyURL); err != nil {
		return nil, err
	}
	a, err := auth.New(st, cfg)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, store: st, version: version, assets: assets, auth: a, limiter: newLimiter(10, 10),
		connect:        publish.Connect,
		newLLM:         func() (llm.Provider, error) { return llm.New(cfg.LLM) },
		loadRedditPost: redditbrowser.Details,
		openRedditBrowser: func(ctx context.Context, proxy string, login bool) (redditBrowser, error) {
			return redditbrowser.Open(ctx, proxy, login)
		},
		newPublisher:   publish.New,
		redditExchange: func(ctx context.Context, r *publish.Reddit, code string) error { return r.ExchangeCode(ctx, code) },
	}
	s.outbox = outbox.New(st, version)
	s.outbox.NewPublisher = func(platform string, credentials json.RawMessage, version string) (publish.Publisher, error) {
		return s.newPublisher(platform, credentials, version)
	}
	return s, nil
}

// Outbox is the publishing service used by the API and background checks.
func (s *Server) Outbox() *outbox.Service { return s.outbox }

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(securityHeaders, sameOrigin)
	r.Get("/health", s.health)
	r.Get(redditCallbackPath, s.redditCallback)
	r.Route("/api", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Get("/config", s.authConfig)
			r.With(s.rateLimit).Post("/register", s.register)
			r.With(s.rateLimit).Post("/login", s.login)
			r.With(s.rateLimit).Post("/refresh", s.refresh)
			r.Post("/logout", s.logout)
			r.With(s.requireAuth).Get("/me", s.me)
		})
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/meta", s.meta)
			r.Get("/queries", s.queries)
			r.Get("/tracking", s.tracking)
			r.Get("/projects", s.projects)
			r.Post("/projects", s.createProject)
			r.Get("/projects/{id}", s.project)
			r.Patch("/projects/{id}", s.renameProject)
			r.Delete("/projects/{id}", s.deleteProject)
			r.Get("/projects/{id}/keywords", s.keywords)
			r.Get("/projects/{id}/reply-settings", s.getReplySettings)
			r.Put("/projects/{id}/reply-settings", s.saveReplySettings)
			r.Post("/projects/{id}/keywords", s.addKeywords)
			r.Delete("/projects/{id}/keywords/{kid}", s.removeKeyword)
			r.Get("/projects/{id}/accounts", s.projectAccounts)
			r.Put("/projects/{id}/accounts/{platform}", s.bindAccount)
			r.Delete("/projects/{id}/accounts/{platform}", s.unbindAccount)
			r.Delete("/projects/{id}/accounts/{platform}/{account_id}", s.unbindProjectAccount)
			r.Get("/summary", s.summary)
			r.Get("/mentions", s.mentions)
			r.Get("/mentions/{mention}/reddit", s.redditPostDetails)
			r.Post("/mentions/{mention}/reply", s.prepareReply)
			r.Post("/track", s.track)
			r.Get("/report", s.report)
			r.Get("/export", s.export)
			r.Get("/opportunities", s.opportunities)
			r.Get("/status", s.status)
			r.Get("/activity", s.activity)
			r.Get("/drafts", s.drafts)
			r.Post("/drafts", s.createDraft)
			r.Get("/drafts/counts", s.draftCounts)
			r.Get("/drafts/{id}", s.draft)
			r.Patch("/drafts/{id}", s.editDraft)
			r.Get("/drafts/{id}/warnings", s.draftWarnings)
			r.Post("/drafts/{id}/approve", s.approveDraft)
			r.Post("/drafts/{id}/skip", s.skipDraft)
			r.Post("/drafts/{id}/publish", s.publishDraft)
			r.Get("/stats", s.stats)
			r.Get("/settings/sources", s.listSourceSettings)
			r.Get("/settings/llm", s.getLLMStatus)
			r.With(s.requireAdmin).Post("/settings/llm/check", s.checkLLM)
			r.With(s.requireAdmin).Get("/settings/proxy", s.proxySettings)
			r.With(s.requireAdmin).Put("/settings/proxy", s.saveProxySettings)
			r.With(s.requireAdmin).Delete("/settings/proxy", s.deleteProxySettings)
			r.With(s.requireAdmin).Put("/settings/sources/{name}", s.saveSourceSettings)
			r.With(s.requireAdmin).Delete("/settings/sources/{name}", s.deleteSourceSettings)
			r.Get("/accounts", s.accounts)
			r.Post("/accounts", s.connectAccount)
			r.Post("/accounts/import", s.importAccounts)
			r.Get("/accounts/stats", s.accountStats)
			r.Post("/accounts/check", s.checkAllAccounts)
			r.Patch("/accounts/{id}", s.updateAccount)
			r.Post("/accounts/{id}/check", s.checkAccount)
			r.Delete("/accounts/{id}", s.deleteAccount)
			r.Post("/accounts/reddit/authorize", s.redditAuthorize)
			r.Get("/accounts/reddit/app", s.redditApp)
			r.Get("/accounts/reddit/browser", s.redditBrowserAvailable)
			r.Post("/accounts/reddit/browser", s.startRedditBrowser)
			r.Post("/accounts/reddit/browser/{session}/input", s.redditBrowserInput)
			r.Post("/accounts/reddit/browser/{session}/finish", s.finishRedditBrowser)
			r.Delete("/accounts/reddit/browser/{session}", s.cancelRedditBrowser)
			r.Put("/accounts/{id}/browser/proxy", s.updateRedditBrowserProxy)
		})
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	})
	r.NotFound(s.spa)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Check(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "database": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "database": "ok"})
}

type sourceInfo struct {
	sources.Info
	Configured bool `json:"configured"`
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	opts, err := pipeline.SourceOptions(s.cfg, s.store, userID(r), 0)
	if err != nil {
		internalError(w, err)
		return
	}
	var list []sourceInfo
	for _, info := range sources.All() {
		list = append(list, sourceInfo{info, sources.Configured(info.Name, opts)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.version,
		"sources":         list,
		"default_sources": s.cfg.Sources,
	})
}

func (s *Server) queries(w http.ResponseWriter, r *http.Request) {
	pid, ok := optionalID(w, r.URL.Query().Get("p"))
	if !ok || (pid != 0 && !s.ownProject(w, r, pid)) {
		return
	}
	qs, err := s.store.Queries(userID(r), pid)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, qs)
}

func (s *Server) tracking(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusUnprocessableEntity, "q is required")
		return
	}
	owns, err := s.store.OwnsQuery(userID(r), q)
	if err != nil {
		internalError(w, err)
		return
	}
	if !owns {
		writeError(w, http.StatusNotFound, "keyword is not tracked")
		return
	}
	t, err := s.store.Tracking(q)
	if err != nil {
		internalError(w, err)
		return
	}
	if t == nil {
		writeError(w, http.StatusNotFound, "keyword is not tracked")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.Projects(userID(r))
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.store.Project(userID(r), id)
	if err != nil {
		internalError(w, err)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := s.store.CreateProject(userID(r), body.Name)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	_ = s.store.LogActivity(userID(r), "project.created", 0, p.Name)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	deleted, err := s.store.DeleteProject(userID(r), id)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	_ = s.store.LogActivity(userID(r), "project.deleted", 0, fmt.Sprintf("project %d", id))
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := s.store.RenameProject(userID(r), id, body.Name)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) keywords(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ks, err := s.store.Keywords(userID(r), id)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ks)
}

// addKeywords adds one keyword or a batch. New keywords are registered for
// scanning with the given sources, or the instance defaults.
func (s *Server) addKeywords(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		Sources []string `json:"sources"`
	}
	if !decode(w, r, &body) {
		return
	}
	queries := body.Queries
	if body.Query != "" {
		queries = append(queries, body.Query)
	}
	if len(queries) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "query or queries is required")
		return
	}
	srcs := body.Sources
	if len(srcs) == 0 {
		srcs = s.cfg.Sources
	}
	for _, n := range srcs {
		if _, ok := sources.Lookup(strings.ToLower(strings.TrimSpace(n))); !ok {
			writeError(w, http.StatusUnprocessableEntity, "unknown source: "+n)
			return
		}
	}
	added, err := s.store.AddKeywords(userID(r), id, queries, srcs)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	if added > 0 {
		_ = s.store.LogActivity(userID(r), "keyword.added", 0, fmt.Sprintf("%d keyword(s) to project %d", added, id))
	}
	ks, err := s.store.Keywords(userID(r), id)
	if err != nil {
		internalError(w, err)
		return
	}
	status := http.StatusOK
	if added > 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"added": added, "keywords": ks})
}

func (s *Server) removeKeyword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kid, ok := parseID(chi.URLParam(r, "kid"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid keyword id")
		return
	}
	removed, err := s.store.RemoveKeyword(userID(r), id, kid)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "keyword not found")
		return
	}
	_ = s.store.LogActivity(userID(r), "keyword.removed", 0, fmt.Sprintf("keyword %d from project %d", kid, id))
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) scope(w http.ResponseWriter, r *http.Request) (store.Scope, bool) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	pid, ok := optionalID(w, r.URL.Query().Get("p"))
	if !ok {
		return store.Scope{}, false
	}
	if q != "" && pid != 0 {
		writeError(w, http.StatusUnprocessableEntity, "choose a keyword or a project, not both")
		return store.Scope{}, false
	}
	if pid != 0 && !s.ownProject(w, r, pid) {
		return store.Scope{}, false
	}
	return store.Scope{UserID: userID(r), Query: q, ProjectID: pid}, true
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	sum, err := s.store.Summary(sc)
	if err != nil {
		internalError(w, err)
		return
	}
	ts, err := s.store.Timeseries(sc)
	if err != nil {
		internalError(w, err)
		return
	}
	themes, err := s.store.Themes(sc, 8)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary":    sum,
		"timeseries": ts,
		"net":        store.NetSentiment(sum),
		"themes":     themes,
	})
}

// MentionView is a mention plus its source's display metadata.
type MentionView struct {
	*model.Mention
	Sentiment   *model.Sentiment `json:"sentiment"`
	SourceLabel string           `json:"source_label"`
	Glyph       string           `json:"glyph"`
	Color       string           `json:"color"`
}

// View decorates a mention for the dashboard.
func View(m *model.Mention) MentionView {
	v := MentionView{Mention: m, SourceLabel: m.Source, Glyph: "•", Color: "#8b93a1"}
	if info, ok := sources.Lookup(m.Source); ok {
		v.SourceLabel, v.Glyph, v.Color = info.Label, info.Glyph, info.Color
	}
	if m.Sentiment != "" {
		sent := m.Sentiment
		v.Sentiment = &sent
	}
	return v
}

func (s *Server) mentions(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	f := store.MentionFilter{Scope: sc, Source: r.URL.Query().Get("source"), Limit: 200}
	if raw := r.URL.Query().Get("sentiment"); raw != "" {
		sent, ok := model.ParseSentiment(raw)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "sentiment must be positive, neutral or negative")
			return
		}
		f.Sentiment = sent
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusUnprocessableEntity, "limit must be between 1 and 1000")
			return
		}
		f.Limit = n
	}
	rows, err := s.store.Mentions(f)
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]MentionView, len(rows))
	for i, m := range rows {
		out[i] = View(m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) track(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query     string   `json:"query"`
		Sources   []string `json:"sources"`
		Mode      string   `json:"mode"`
		Pages     int      `json:"pages"`
		Limit     int      `json:"limit"`
		ProjectID *int64   `json:"project_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	query := strings.TrimSpace(body.Query)
	if query == "" {
		writeError(w, http.StatusUnprocessableEntity, "query must not be empty")
		return
	}
	var names []string
	for _, n := range body.Sources {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if _, ok := sources.Lookup(n); !ok {
			writeError(w, http.StatusUnprocessableEntity, "unknown source: "+n)
			return
		}
		names = append(names, n)
	}
	if len(body.Sources) == 0 {
		// Rescan with what the keyword was tracked with, else the defaults.
		names = s.cfg.Sources
		if t, err := s.store.Tracking(query); err != nil {
			internalError(w, err)
			return
		} else if t != nil && len(t.Sources) > 0 {
			names = t.Sources
		}
	}
	if len(names) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "select at least one source")
		return
	}
	if body.Limit != 0 && (body.Limit < 1 || body.Limit > 100) {
		writeError(w, http.StatusUnprocessableEntity, "limit must be between 1 and 100")
		return
	}
	if body.Mode == "" {
		body.Mode = "incremental"
	}
	if body.Mode != "incremental" && body.Mode != "backfill" {
		writeError(w, http.StatusUnprocessableEntity, "mode must be incremental or backfill")
		return
	}
	if body.Pages == 0 {
		body.Pages = 3
	}
	if body.Pages < 1 || body.Pages > 20 {
		writeError(w, http.StatusUnprocessableEntity, "pages must be between 1 and 20")
		return
	}
	var pid int64
	if body.ProjectID != nil {
		pid = *body.ProjectID
		if !s.ownProject(w, r, pid) {
			return
		}
	}
	cfg := *s.cfg
	cfg.Sources = names
	if body.Limit != 0 {
		cfg.PerSourceLimit = body.Limit
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := pipeline.New(&cfg, s.store).Track(ctx, query, pipeline.Options{
		Backfill: body.Mode == "backfill", Pages: body.Pages, UserID: userID(r), ProjectID: pid,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ownProject answers 404 unless the project belongs to the signed-in user,
// so ids of other users' projects cannot be probed.
func (s *Server) ownProject(w http.ResponseWriter, r *http.Request, id int64) bool {
	p, err := s.store.Project(userID(r), id)
	if err != nil {
		internalError(w, err)
		return false
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return false
	}
	return true
}

// spa serves the built dashboard, falling back to index.html for client routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if s.assets == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if st, err := fs.Stat(s.assets, name); err != nil || st.IsDir() {
		name = "index.html"
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, s.assets, name)
}

// --- middleware & helpers ---------------------------------------------------

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects cross-site state-changing requests. Non-browser clients
// (CLI, curl) send no Origin and pass; a browser always sends Origin on a
// cross-site POST/DELETE.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeError(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func internalError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, err.Error())
}

func storeError(w http.ResponseWriter, err error, fallback int) {
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, fallback, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func parseID(raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id >= 1
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid project id")
	}
	return id, ok
}

func optionalID(w http.ResponseWriter, raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	id, ok := parseID(raw)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid project id")
	}
	return id, ok
}
