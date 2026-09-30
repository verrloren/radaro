package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/sources"
	"github.com/verrloren/radaro/internal/store"
)

// --- reports ----------------------------------------------------------------------

// Report is a keyword's or project's sentiment and themes with a few examples.
type Report struct {
	Query       string             `json:"query,omitempty"`
	Summary     store.Summary      `json:"summary"`
	Net         float64            `json:"net"`
	Themes      []store.ThemeCount `json:"themes"`
	TopPositive []*model.Mention   `json:"top_positive"`
	TopNegative []*model.Mention   `json:"top_negative"`
}

// BuildReport aggregates a scope.
func BuildReport(st *store.Store, sc store.Scope) (*Report, error) {
	sum, err := st.Summary(sc)
	if err != nil {
		return nil, err
	}
	themes, err := st.Themes(sc, 6)
	if err != nil {
		return nil, err
	}
	r := &Report{Query: sc.Query, Summary: sum, Net: store.NetSentiment(sum), Themes: themes,
		TopPositive: []*model.Mention{}, TopNegative: []*model.Mention{}}
	for _, s := range []model.Sentiment{model.Positive, model.Negative} {
		ms, err := st.Mentions(store.MentionFilter{Scope: sc, Sentiment: s, Limit: 2})
		if err != nil {
			return nil, err
		}
		if s == model.Positive {
			r.TopPositive = append(r.TopPositive, ms...)
		} else {
			r.TopNegative = append(r.TopNegative, ms...)
		}
	}
	return r, nil
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	rep, err := BuildReport(s.store, sc)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// export returns every mention in scope, complete, for backups and spreadsheets.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	rows, err := s.store.Mentions(store.MentionFilter{Scope: sc})
	if err != nil {
		internalError(w, err)
		return
	}
	if rows == nil {
		rows = []*model.Mention{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// opportunities are recent mentions with a link and no draft yet.
func (s *Server) opportunities(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(w, r)
	if !ok {
		return
	}
	days, ok := intParam(w, r, "days", 14, 1, 365)
	if !ok {
		return
	}
	limit, ok := intParam(w, r, "limit", 20, 1, 200)
	if !ok {
		return
	}
	mentions, err := s.store.Mentions(store.MentionFilter{Scope: sc, Source: r.URL.Query().Get("source")})
	if err != nil {
		internalError(w, err)
		return
	}
	drafted, err := s.store.DraftedMentionIDs(userID(r))
	if err != nil {
		internalError(w, err)
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	out := []MentionView{}
	for _, m := range mentions {
		if m.CreatedAt.Before(cutoff) || drafted[m.ID] || m.URL == nil {
			continue
		}
		out = append(out, View(m))
		if len(out) == limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// status is what is set up for the signed-in user.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	u, err := s.store.User(uid)
	if err != nil || u == nil {
		internalError(w, errors.Join(err, errors.New("user not found")))
		return
	}
	accounts, err := s.store.Accounts(uid, "")
	if err != nil {
		internalError(w, err)
		return
	}
	projects, err := s.store.Projects(uid)
	if err != nil {
		internalError(w, err)
		return
	}
	queries, err := s.store.Queries(uid, 0)
	if err != nil {
		internalError(w, err)
		return
	}
	drafts, err := s.store.DraftCounts(uid)
	if err != nil {
		internalError(w, err)
		return
	}
	opts, err := pipeline.SourceOptions(s.cfg, s.store, uid, 0)
	if err != nil {
		internalError(w, err)
		return
	}
	type sourceStatus struct {
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
	}
	var srcs []sourceStatus
	for _, info := range sources.All() {
		srcs = append(srcs, sourceStatus{info.Name, sources.Configured(info.Name, opts)})
	}
	out := map[string]any{
		"version":         s.version,
		"user":            u,
		"default_sources": s.cfg.Sources,
		"sources":         srcs,
		"accounts":        accounts,
		"projects":        len(projects),
		"keywords":        queries,
		"drafts":          drafts,
	}
	if u.IsAdmin {
		out["database"] = s.store.Path()
		out["data_dir"] = config.DataDir()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit", 50, 1, 1000)
	if !ok {
		return
	}
	acts, err := s.store.Activities(userID(r), limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acts)
}

// --- drafts -----------------------------------------------------------------------

func (s *Server) drafts(w http.ResponseWriter, r *http.Request) {
	limit, ok := intParam(w, r, "limit", 50, 1, 1000)
	if !ok {
		return
	}
	ds, err := s.store.Drafts(userID(r), r.URL.Query().Get("status"), limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

// createDraft writes a post or reply. With a mention and no reply_to, a reply
// answers the thread the mention came from.
func (s *Server) createDraft(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID int64  `json:"project_id"`
		Platform  string `json:"platform"`
		AccountID int64  `json:"account_id"`
		Kind      string `json:"kind"`
		Community string `json:"community"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		ReplyTo   string `json:"reply_to"`
		Query     string `json:"query"`
		MentionID string `json:"mention_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	uid := userID(r)
	pl, ok := publish.LookupPlatform(body.Platform)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "unknown platform: "+body.Platform)
		return
	}
	if body.MentionID != "" {
		ms, err := s.store.Mentions(store.MentionFilter{Scope: store.Scope{UserID: uid}, ID: body.MentionID, Limit: 1})
		if err != nil {
			internalError(w, err)
			return
		}
		if len(ms) == 0 {
			writeError(w, http.StatusNotFound, "mention "+body.MentionID+" not found")
			return
		}
		if m := ms[0]; body.ReplyTo == "" && m.Source == body.Platform && m.URL != nil {
			body.ReplyTo = *m.URL
		}
		if body.Query == "" {
			body.Query = ms[0].Query
		}
	}
	if body.Kind == "" {
		body.Kind = "post"
		if body.ReplyTo != "" {
			body.Kind = "reply"
		}
	}
	if err := pl.Validate(publish.Post{Kind: body.Kind, Community: body.Community, Title: body.Title, Body: body.Body, ReplyTo: body.ReplyTo}); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	d, err := s.store.CreateDraft(store.NewDraft{
		UserID: uid, ProjectID: body.ProjectID, Platform: body.Platform, AccountID: body.AccountID, Kind: body.Kind,
		Community: body.Community, Title: body.Title, Body: body.Body, ReplyTo: body.ReplyTo, Query: body.Query, MentionID: body.MentionID,
	})
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	_ = s.store.LogActivity(uid, "draft.created", d.ID, DraftSummary(d))
	writeJSON(w, http.StatusCreated, d)
}

// draftFor loads the path's draft of the signed-in user, or answers 404.
func (s *Server) draftFor(w http.ResponseWriter, r *http.Request) (*store.Draft, bool) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid draft id")
		return nil, false
	}
	d, err := s.store.Draft(userID(r), id)
	if err != nil {
		internalError(w, err)
		return nil, false
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "draft not found")
		return nil, false
	}
	return d, true
}

func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.draftFor(w, r); ok {
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Server) editDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	var e struct {
		Title     *string `json:"title"`
		Body      *string `json:"body"`
		Community *string `json:"community"`
	}
	if !decode(w, r, &e) {
		return
	}
	updated, err := s.store.EditDraft(userID(r), d.ID, store.DraftEdit{Title: e.Title, Body: e.Body, Community: e.Community})
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	_ = s.store.LogActivity(userID(r), "draft.edited", d.ID, DraftSummary(updated))
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) approveDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	if err := ValidateDraft(d); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	d, err := s.store.ApproveDraft(userID(r), d.ID)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	_ = s.store.LogActivity(userID(r), "draft.approved", d.ID, DraftSummary(d))
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) skipDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	d, err := s.store.SkipDraft(userID(r), d.ID)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	_ = s.store.LogActivity(userID(r), "draft.skipped", d.ID, DraftSummary(d))
	writeJSON(w, http.StatusOK, d)
}

// publishDraft sends an approved draft. The draft is claimed before any
// network call, so a crash leaves it visibly "publishing" instead of posting
// twice on a retry.
func (s *Server) publishDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	uid := userID(r)
	if d.Status != store.DraftApproved {
		writeError(w, http.StatusConflict, fmt.Sprintf("draft %d is not approved (status: %s)", d.ID, d.Status))
		return
	}
	if err := ValidateDraft(d); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	acc, err := s.store.PublishAccount(uid, d)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return
	}
	pub, err := s.newPublisher(acc.Platform, acc.Credentials, s.version)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, err := s.store.BeginPublish(uid, d.ID); err != nil {
		storeError(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	res, pubErr := pub.Publish(ctx, PostOf(d))
	d, err = s.store.FinishPublish(d.ID, res.RemoteID, res.URL, pubErr)
	if err != nil {
		internalError(w, err)
		return
	}
	if pubErr != nil {
		_ = s.store.LogActivity(uid, "draft.failed", d.ID, pubErr.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "publishing failed: " + pubErr.Error(), "draft": d})
		return
	}
	_ = s.store.LogActivity(uid, "draft.published", d.ID, acc.Platform+" "+acc.Handle+" "+res.URL)
	writeJSON(w, http.StatusOK, d)
}

// stats lists published drafts, with ?refresh=true fetching fresh metrics.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	ds, err := s.store.Drafts(uid, store.DraftPublished, 0)
	if err != nil {
		internalError(w, err)
		return
	}
	problems := []string{}
	if refresh, _ := strconv.ParseBool(r.URL.Query().Get("refresh")); refresh {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		for _, d := range ds {
			if d.RemoteID == nil {
				continue
			}
			acc, err := s.store.PublishAccount(uid, d)
			if err != nil {
				problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
				continue
			}
			pub, err := s.newPublisher(acc.Platform, acc.Credentials, s.version)
			if err != nil {
				problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
				continue
			}
			m, err := pub.Metrics(ctx, *d.RemoteID)
			if err != nil {
				problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
				continue
			}
			if err := s.store.SaveMetrics(d.ID, m); err != nil {
				internalError(w, err)
				return
			}
		}
		if ds, err = s.store.Drafts(uid, store.DraftPublished, 0); err != nil {
			internalError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"published": ds, "errors": problems})
}

// PostOf turns a draft into what a platform receives. The idempotency key
// lets platforms that support one drop a duplicate.
func PostOf(d *store.Draft) publish.Post {
	return publish.Post{Kind: d.Kind, Community: deref(d.Community), Title: deref(d.Title), Body: d.Body,
		ReplyTo: deref(d.ReplyTo), IdempotencyKey: fmt.Sprintf("radaro-draft-%d", d.ID)}
}

// ValidateDraft checks a draft against its platform's rules.
func ValidateDraft(d *store.Draft) error {
	pl, ok := publish.LookupPlatform(d.Platform)
	if !ok {
		return fmt.Errorf("unknown platform %q", d.Platform)
	}
	return pl.Validate(PostOf(d))
}

// DraftSummary is a one-line description for the activity log.
func DraftSummary(d *store.Draft) string {
	where := d.Platform
	if d.Community != nil && d.Platform == "reddit" {
		where += " r/" + strings.TrimPrefix(*d.Community, "r/")
	}
	if d.Kind == "reply" {
		return where + " reply to " + deref(d.ReplyTo)
	}
	if d.Title != nil {
		return where + " post “" + clip(*d.Title, 60) + "”"
	}
	return where + " post “" + clip(d.Body, 60) + "”"
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func intParam(w http.ResponseWriter, r *http.Request, name string, def, lo, hi int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s must be between %d and %d", name, lo, hi))
		return 0, false
	}
	return n, true
}
