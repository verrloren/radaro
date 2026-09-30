package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/verrloren/radaro/internal/outbox"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

// The publishing queue: a draft with its publish plan, edits, approval and
// publishing through the outbox, which enforces every account limit.

var draftStatuses = []string{store.DraftPending, store.DraftApproved, store.DraftPublishing,
	store.DraftPublished, store.DraftFailed, store.DraftSkipped}

const errDraftNotFound = "draft not found"

const (
	// warningsTimeout bounds the review, which may ask Reddit for a subreddit's rules.
	warningsTimeout = 10 * time.Second
	// publishTimeout bounds a publish; it runs to the end even if the client goes away.
	publishTimeout = 2 * time.Minute
)

// publishPlan is who would publish a draft now, and when it may go out.
type publishPlan struct {
	Account *accountView  `json:"account"` // nil when no account can publish it
	Quota   *outbox.Quota `json:"quota"`   // the account's standing for this draft
	NextAt  *time.Time    `json:"next_at"` // nil = now, or only a person can unblock it (see reason)
	Reason  string        `json:"reason,omitempty"`
}

// draftDetail is a draft with its plan, for drafts that can still be published.
type draftDetail struct {
	*store.Draft
	Plan *publishPlan `json:"plan"`
}

// detail decorates one of the user's drafts with its plan.
func (s *Server) detail(uid int64, d *store.Draft) draftDetail {
	return draftDetail{Draft: d, Plan: s.plan(uid, d)}
}

// plan is nil for drafts that are published, being published or skipped.
func (s *Server) plan(uid int64, d *store.Draft) *publishPlan {
	if d.Status != store.DraftPending && d.Status != store.DraftApproved && d.Status != store.DraftFailed {
		return nil
	}
	p := &publishPlan{}
	acc, err := s.outbox.PickAccount(uid, d)
	p.blocked(err)
	if acc == nil && d.AccountID != nil {
		acc, _ = s.store.Account(uid, *d.AccountID)
	}
	if acc != nil {
		s.planAccount(uid, p, acc, d)
	}
	return p
}

// blocked records why no account can publish the draft now.
func (p *publishPlan) blocked(err error) {
	var qe *outbox.QuotaError
	switch {
	case errors.As(err, &qe):
		p.NextAt, p.Reason = timeOrNil(qe.NextAt), qe.Reason
	case err != nil:
		p.Reason = err.Error()
	}
}

// planAccount fills in the account the draft would go out from and where it
// stands for this draft.
func (s *Server) planAccount(uid int64, p *publishPlan, acc *store.Account, d *store.Draft) {
	v := accountView{Account: acc}
	if full, err := s.accountView(uid, acc); err == nil {
		v = full
	}
	p.Account = &v
	q, err := s.outbox.Quota(acc, deref(d.Community), deref(d.ReplyTo))
	if err != nil {
		if p.Reason == "" {
			p.Reason = err.Error()
		}
		return
	}
	p.Quota = &q
	if p.NextAt == nil {
		p.NextAt = q.NextAt
	}
	if p.Reason == "" {
		p.Reason = q.Reason
	}
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// draftCounts answers how many of the user's drafts are in each status.
func (s *Server) draftCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.DraftCounts(userID(r))
	if err != nil {
		internalError(w, err)
		return
	}
	for _, st := range draftStatuses {
		counts[st] += 0 // every status is listed, empty ones as 0
	}
	writeJSON(w, http.StatusOK, counts)
}

func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.draftFor(w, r); ok {
		writeJSON(w, http.StatusOK, s.detail(userID(r), d))
	}
}

// editDraft changes a draft's text or account. The result is checked with
// the platform before anything is saved, and any change sends the draft back
// to review.
func (s *Server) editDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	c, err := parseDraftChange(raw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if d.Status == store.DraftPublished || d.Status == store.DraftPublishing {
		writeError(w, http.StatusConflict, fmt.Sprintf("draft %d is already %s", d.ID, d.Status))
		return
	}
	if err := ValidateDraft(c.preview(d)); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	updated, ok := s.applyDraftChange(w, r, d, c)
	if !ok {
		return
	}
	_ = s.store.LogActivity(userID(r), "draft.edited", d.ID, DraftSummary(updated))
	writeJSON(w, http.StatusOK, s.detail(userID(r), updated))
}

// applyDraftChange saves a checked change, or writes the error.
func (s *Server) applyDraftChange(w http.ResponseWriter, r *http.Request, d *store.Draft, c draftChange) (*store.Draft, bool) {
	uid := userID(r)
	if c.setAccount {
		if !s.draftAccountOK(w, r, d, c.accountID) {
			return nil, false
		}
		if _, err := s.store.SetDraftAccount(uid, d.ID, c.accountID); err != nil {
			storeError(w, err, http.StatusInternalServerError)
			return nil, false
		}
	}
	updated, err := s.store.EditDraft(uid, d.ID, c.edit)
	if err != nil {
		storeError(w, err, http.StatusUnprocessableEntity)
		return nil, false
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, errDraftNotFound)
		return nil, false
	}
	return updated, true
}

// draftChange is a PATCH /api/drafts/{id} body. accountID nil with
// setAccount means pick an account automatically.
type draftChange struct {
	edit       store.DraftEdit
	setAccount bool
	accountID  *int64
}

// parseDraftChange reads an edit; the error is the message for the client.
func parseDraftChange(raw map[string]json.RawMessage) (draftChange, error) {
	var c draftChange
	if len(raw) == 0 {
		return c, errors.New("nothing to change")
	}
	for key, val := range raw {
		switch key {
		case "title", "body", "community":
			if err := c.setText(key, val); err != nil {
				return c, err
			}
		case "account_id":
			c.setAccount = true
			if err := json.Unmarshal(val, &c.accountID); err != nil || (c.accountID != nil && *c.accountID < 1) {
				return c, errors.New("account_id must be an account id, or null to pick one automatically")
			}
		default:
			return c, errors.New("unknown field: " + key)
		}
	}
	return c, nil
}

func (c *draftChange) setText(key string, val json.RawMessage) error {
	var v string
	if err := json.Unmarshal(val, &v); err != nil {
		return errors.New(key + " must be a string")
	}
	switch key {
	case "title":
		c.edit.Title = &v
	case "body":
		c.edit.Body = &v
	default:
		c.edit.Community = &v
	}
	return nil
}

// preview is d as the change would leave it.
func (c draftChange) preview(d *store.Draft) *store.Draft {
	next := *d
	if c.edit.Title != nil {
		next.Title = c.edit.Title
	}
	if c.edit.Body != nil {
		next.Body = *c.edit.Body
	}
	if c.edit.Community != nil {
		next.Community = c.edit.Community
	}
	return &next
}

// draftAccountOK checks that id, if set, is one of the user's accounts on
// d's platform, or writes the error: 404 for an unknown or foreign account.
func (s *Server) draftAccountOK(w http.ResponseWriter, r *http.Request, d *store.Draft, id *int64) bool {
	if id == nil {
		return true
	}
	acc, err := s.store.Account(userID(r), *id)
	if err != nil {
		internalError(w, err)
		return false
	}
	if acc == nil {
		writeError(w, http.StatusNotFound, errAccountNotFound)
		return false
	}
	if acc.Platform != d.Platform {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("account %d is not a %s account", *id, d.Platform))
		return false
	}
	return true
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
	writeJSON(w, http.StatusOK, s.detail(userID(r), d))
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
	writeJSON(w, http.StatusOK, draftDetail{Draft: d})
}

// publishDraft sends an approved draft through the outbox: the account is
// picked and the draft claimed within its limits before any network call.
func (s *Server) publishDraft(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	// Once started, the publish finishes even if the client goes away, so
	// its outcome is recorded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), publishTimeout)
	defer cancel()
	published, acc, err := s.outbox.Publish(ctx, userID(r), d.ID)
	if err != nil {
		publishError(w, published, acc, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": published, "account": acc})
}

// publishError answers a publish that did not go through: 409 with next_at
// for a limit or a platform rate limit (the draft stays approved), 502 when
// the platform refused it (the draft failed), 422 when nothing was sent.
func publishError(w http.ResponseWriter, d *store.Draft, acc *store.Account, err error) {
	body := map[string]any{"error": err.Error(), "draft": d, "account": acc}
	var qe *outbox.QuotaError
	var rl *publish.RateLimitError
	switch {
	case errors.As(err, &qe):
		body["next_at"] = timeOrNil(qe.NextAt)
		writeJSON(w, http.StatusConflict, body)
	case errors.As(err, &rl):
		body["next_at"] = retryAt(acc, rl)
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, errDraftNotFound)
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, body)
	case d != nil && d.Status == store.DraftFailed:
		body["error"] = "publishing failed: " + err.Error()
		writeJSON(w, http.StatusBadGateway, body)
	default:
		writeJSON(w, http.StatusUnprocessableEntity, body)
	}
}

// retryAt is when a rate-limited account may publish again.
func retryAt(acc *store.Account, rl *publish.RateLimitError) time.Time {
	if acc != nil && acc.LimitedUntil != nil {
		return store.ParseStamp(*acc.LimitedUntil)
	}
	return time.Now().Add(rl.RetryAfter)
}

func (s *Server) draftWarnings(w http.ResponseWriter, r *http.Request) {
	d, ok := s.draftFor(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), warningsTimeout)
	defer cancel()
	ws, err := s.outbox.Warnings(ctx, userID(r), d)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// stats lists published drafts, with ?refresh=true fetching fresh metrics
// (and recording removals) first.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	problems := []string{}
	if refresh, _ := strconv.ParseBool(r.URL.Query().Get("refresh")); refresh {
		ctx, cancel := context.WithTimeout(r.Context(), publishTimeout)
		defer cancel()
		found, err := s.outbox.RefreshMetrics(ctx, uid)
		if err != nil {
			internalError(w, err)
			return
		}
		problems = append(problems, found...)
	}
	ds, err := s.store.Drafts(uid, store.DraftPublished, 0)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"published": ds, "errors": problems})
}
