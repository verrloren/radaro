package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/verrloren/radaro/internal/outbox"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

const (
	errAccountNotFound = "account not found"
	errInvalidAccount  = "invalid account id"
)

// Upper bounds for per-account limits; beyond them a value is a typo, not a policy.
const (
	maxDailyLimit        = 1000
	maxMinIntervalSec    = 7 * 24 * 3600
	maxCommunityCooldown = 90 * 24
)

// limitsView is a platform's default limits in JSON-friendly units.
type limitsView struct {
	Daily              int `json:"daily"`
	MinIntervalSec     int `json:"min_interval_sec"`
	CommunityCooldownH int `json:"community_cooldown_h"`
}

// accountView is an account with its effective limits, where it stands
// against them and what it has published. Credentials are never part of it
// (json:"-" on store.Account).
type accountView struct {
	*store.Account
	Limits        outbox.LimitsView     `json:"limits"`
	Quota         *outbox.Quota         `json:"quota,omitempty"`
	Activity      store.AccountActivity `json:"activity"`
	DefaultLimits limitsView            `json:"default_limits"`
}

// accountViews decorates the user's accounts.
func (s *Server) accountViews(uid int64, accs []*store.Account) ([]accountView, error) {
	activity, err := s.store.AccountActivity(uid, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]accountView, 0, len(accs))
	for _, acc := range accs {
		v := accountView{Account: acc, Limits: outbox.ViewLimits(acc), Activity: activity[acc.ID]}
		v.Activity.AccountID = acc.ID
		// A quota that cannot be computed is left out rather than failing the list.
		if q, err := s.outbox.Quota(acc, "", ""); err == nil {
			v.Quota = &q
		}
		d := publish.DefaultLimits(acc.Platform)
		v.DefaultLimits = limitsView{Daily: d.Daily, MinIntervalSec: int(d.MinInterval / time.Second), CommunityCooldownH: int(d.CommunityCooldown / time.Hour)}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) accountView(uid int64, acc *store.Account) (accountView, error) {
	vs, err := s.accountViews(uid, []*store.Account{acc})
	if err != nil {
		return accountView{}, err
	}
	return vs[0], nil
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	s.writeAccountList(w, r, nil)
}

// writeAccountList answers with the user's accounts; a non-nil checkErr is
// reported next to them, as a summary (error) and per account (errors).
func (s *Server) writeAccountList(w http.ResponseWriter, r *http.Request, checkErr error) {
	accs, err := s.store.Accounts(userID(r), "")
	if err != nil {
		internalError(w, err)
		return
	}
	views, err := s.accountViews(userID(r), accs)
	if err != nil {
		internalError(w, err)
		return
	}
	out := map[string]any{"platforms": publish.Platforms, "accounts": views}
	if checkErr != nil {
		failures := []string{}
		for _, ce := range outbox.CheckErrors(checkErr) {
			failures = append(failures, fmt.Sprintf("account %d: %s", ce.AccountID, ce.Error()))
		}
		out["error"], out["errors"] = checkErr.Error(), failures
	}
	writeJSON(w, http.StatusOK, out)
}

// accountFromPath resolves {id} to one of the user's accounts, or writes a 422/404.
func (s *Server) accountFromPath(w http.ResponseWriter, r *http.Request) (*store.Account, bool) {
	id, ok := parseID(chi.URLParam(r, "id"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, errInvalidAccount)
		return nil, false
	}
	acc, err := s.store.Account(userID(r), id)
	if err != nil {
		internalError(w, err)
		return nil, false
	}
	if acc == nil {
		writeError(w, http.StatusNotFound, errAccountNotFound)
		return nil, false
	}
	return acc, true
}

// accountChange is a PATCH /api/accounts/{id} body; 0 resets a limit to the
// platform default.
type accountChange struct {
	Paused             *bool `json:"paused"`
	DailyLimit         *int  `json:"daily_limit"`
	MinIntervalSec     *int  `json:"min_interval_sec"`
	CommunityCooldownH *int  `json:"community_cooldown_h"`
}

// limitFields names the change's limits with their upper bounds.
func (c accountChange) limitFields() []struct {
	name string
	v    *int
	max  int
} {
	return []struct {
		name string
		v    *int
		max  int
	}{
		{"daily_limit", c.DailyLimit, maxDailyLimit},
		{"min_interval_sec", c.MinIntervalSec, maxMinIntervalSec},
		{"community_cooldown_h", c.CommunityCooldownH, maxCommunityCooldown},
	}
}

// validate returns the message for a change that must be refused, or "".
func (c accountChange) validate() string {
	empty := c.Paused == nil
	for _, f := range c.limitFields() {
		if f.v == nil {
			continue
		}
		empty = false
		if *f.v < 0 || *f.v > f.max {
			return fmt.Sprintf("%s must be between 0 and %d (0 = platform default)", f.name, f.max)
		}
	}
	if empty {
		return "nothing to update"
	}
	return ""
}

// describe is the change for the activity log.
func (c accountChange) describe(acc *store.Account) string {
	parts := []string{acc.Platform + " " + acc.Handle}
	if c.Paused != nil {
		parts = append(parts, "paused="+strconv.FormatBool(*c.Paused))
	}
	for _, f := range c.limitFields() {
		if f.v != nil {
			parts = append(parts, f.name+"="+strconv.Itoa(*f.v))
		}
	}
	return strings.Join(parts, " ")
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.accountFromPath(w, r)
	if !ok {
		return
	}
	var c accountChange
	if !decode(w, r, &c) {
		return
	}
	if msg := c.validate(); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	u := store.AccountUpdate{
		Paused: c.Paused, DailyLimit: c.DailyLimit, MinIntervalSec: c.MinIntervalSec, CommunityCooldownH: c.CommunityCooldownH,
	}
	if c.Paused != nil && !*c.Paused && outbox.AutoPaused(acc) {
		// A person resumed it: the automatic pause's explanation is done.
		cleared := ""
		u.StatusDetail = &cleared
	}
	updated, err := s.store.UpdateAccount(userID(r), acc.ID, u)
	if err != nil {
		storeError(w, err, http.StatusInternalServerError)
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, errAccountNotFound)
		return
	}
	_ = s.store.LogActivity(userID(r), "account.updated", 0, c.describe(updated))
	s.writeAccount(w, r, updated)
}

func (s *Server) writeAccount(w http.ResponseWriter, r *http.Request, acc *store.Account) {
	v, err := s.accountView(userID(r), acc)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) checkAccount(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.accountFromPath(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	checked, err := s.outbox.Check(ctx, userID(r), acc.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, errAccountNotFound)
	case err != nil:
		writeError(w, http.StatusBadGateway, "check failed: "+err.Error())
	default:
		s.writeAccount(w, r, checked)
	}
}

// checkAllAccounts checks every account of the user and answers with the
// list. Checks that could not run leave those accounts as they were; error
// says why.
func (s *Server) checkAllAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	err := s.outbox.CheckAll(ctx, userID(r))
	if err != nil && len(outbox.CheckErrors(err)) == 0 {
		// Not one account's check: the accounts could not even be listed.
		internalError(w, err)
		return
	}
	s.writeAccountList(w, r, err)
}

// accountTally counts accounts by health. Platform is empty for the total.
type accountTally struct {
	Platform string  `json:"platform,omitempty"`
	Total    int     `json:"total"`
	Live     int     `json:"live"`
	Dead     int     `json:"dead"` // invalid or suspended
	Limited  int     `json:"limited"`
	Paused   int     `json:"paused"`
	Unknown  int     `json:"unknown"`
	BanRate  float64 `json:"ban_rate"` // dead / total, 0–1
}

func (t *accountTally) add(a *store.Account) {
	t.Total++
	switch a.Status {
	case store.AccountLive:
		t.Live++
	case store.AccountInvalid, store.AccountSuspended:
		t.Dead++
	case store.AccountLimited:
		t.Limited++
	default:
		t.Unknown++
	}
	if a.Paused {
		t.Paused++
	}
}

func (t *accountTally) finish() {
	if t.Total > 0 {
		t.BanRate = float64(t.Dead) / float64(t.Total)
	}
}

// tally counts accs per platform, sorted by platform, and in total.
func tally(accs []*store.Account) ([]*accountTally, *accountTally) {
	by := map[string]*accountTally{}
	total := &accountTally{}
	for _, a := range accs {
		t := by[a.Platform]
		if t == nil {
			t = &accountTally{Platform: a.Platform}
			by[a.Platform] = t
		}
		t.add(a)
		total.add(a)
	}
	platforms := make([]*accountTally, 0, len(by))
	for _, t := range by {
		t.finish()
		platforms = append(platforms, t)
	}
	sort.Slice(platforms, func(i, j int) bool { return platforms[i].Platform < platforms[j].Platform })
	total.finish()
	return platforms, total
}

func (s *Server) accountStats(w http.ResponseWriter, r *http.Request) {
	days, ok := intParam(w, r, "days", 30, 1, 90)
	if !ok {
		return
	}
	series, err := s.store.BanRateSeries(userID(r), days, time.Now())
	if err != nil {
		internalError(w, err)
		return
	}
	accs, err := s.store.Accounts(userID(r), "")
	if err != nil {
		internalError(w, err)
		return
	}
	platforms, total := tally(accs)
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "series": series, "platforms": platforms, "total": total})
}
