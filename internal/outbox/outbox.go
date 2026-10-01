// Package outbox publishes approved drafts within each account's limits and
// keeps account health current. The server runs it for every user: methods
// take the acting user, and user 0 is the server itself (the background
// checks across all users).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

// Service publishes drafts and checks accounts.
type Service struct {
	Store   *store.Store
	Version string
	// NewPublisher builds a connector; tests replace it. Default publish.New.
	NewPublisher func(platform string, credentials json.RawMessage, version string) (publish.Publisher, error)
	// Now is the clock; tests replace it. Default time.Now.
	Now func() time.Time
	// Notify, if set, is called when an account goes dead (invalid or suspended).
	Notify func(ctx context.Context, text string, payload map[string]any) error
	// Logf, if set, receives problems of background runs that have no caller
	// to return them to. Messages never contain credentials.
	Logf func(format string, args ...any)
}

// New returns a Service with the default connector and clock.
func New(st *store.Store, version string) *Service {
	return &Service{Store: st, Version: version, NewPublisher: publish.New, Now: time.Now}
}

// Quota is where an account stands against its limits for one draft.
type Quota struct {
	Limits    publish.Limits `json:"-"`
	Daily     int            `json:"daily"`
	Used24h   int            `json:"used_24h"`
	Remaining int            `json:"remaining"`
	// NextAt is when the account may publish (this draft) next; nil = now.
	// With Ready false and NextAt nil, only a person can unblock it
	// (resume, reconnect, or a different thread).
	NextAt *time.Time `json:"next_at"`
	Reason string     `json:"reason,omitempty"` // why not now
	// Ready reports whether the account may publish (this draft) now.
	Ready bool `json:"ready"`
}

// QuotaError is a publish refused by a limit; nothing was sent.
type QuotaError struct {
	NextAt time.Time // zero when no time will lift it
	Reason string
}

func (e *QuotaError) Error() string {
	if e.NextAt.IsZero() {
		return e.Reason
	}
	return fmt.Sprintf("%s; next possible at %s", e.Reason, e.NextAt.Local().Format("2006-01-02 15:04"))
}

// LimitsView is an account's effective limits in JSON-friendly units.
type LimitsView struct {
	Daily              int  `json:"daily"`
	MinIntervalSec     int  `json:"min_interval_sec"`
	CommunityCooldownH int  `json:"community_cooldown_h"`
	Custom             bool `json:"custom"` // at least one limit overrides the platform default
}

// LimitsOf returns acc's effective limits: its own overrides, else the
// platform defaults.
func LimitsOf(acc *store.Account) publish.Limits {
	l := publish.DefaultLimits(acc.Platform)
	if acc.DailyLimit != nil && *acc.DailyLimit > 0 {
		l.Daily = *acc.DailyLimit
	}
	if acc.MinIntervalSec != nil && *acc.MinIntervalSec > 0 {
		l.MinInterval = time.Duration(*acc.MinIntervalSec) * time.Second
	}
	if acc.CommunityCooldownH != nil && *acc.CommunityCooldownH > 0 {
		l.CommunityCooldown = time.Duration(*acc.CommunityCooldownH) * time.Hour
	}
	return l
}

// ViewLimits returns acc's effective limits for JSON.
func ViewLimits(acc *store.Account) LimitsView {
	l := LimitsOf(acc)
	return LimitsView{
		Daily: l.Daily, MinIntervalSec: int(l.MinInterval / time.Second), CommunityCooldownH: int(l.CommunityCooldown / time.Hour),
		Custom: acc.DailyLimit != nil || acc.MinIntervalSec != nil || acc.CommunityCooldownH != nil,
	}
}

// AccountView is an account with its effective limits and its general quota
// (no community or thread).
type AccountView struct {
	*store.Account
	Limits LimitsView `json:"limits"`
	Quota  Quota      `json:"quota"`
}

// View returns acc with its limits and quota.
func (s *Service) View(acc *store.Account) (AccountView, error) {
	q, err := s.Quota(acc, "", "")
	return AccountView{Account: acc, Limits: ViewLimits(acc), Quota: q}, err
}

// Dead reports whether an account status means it cannot publish until a
// person reconnects it.
func Dead(status string) bool {
	return status == store.AccountInvalid || status == store.AccountSuspended
}

// target is what a publication is aimed at; empty fields are not checked.
type target struct {
	community, replyTo, title string
}

func targetOf(d *store.Draft) target {
	return target{community: deref(d.Community), replyTo: deref(d.ReplyTo), title: deref(d.Title)}
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// Quota reports acc's standing for a publication in community / replying to
// replyTo (both may be empty for a general answer). "Another account" in the
// rules is always another account of acc's owner.
func (s *Service) Quota(acc *store.Account, community, replyTo string) (Quota, error) {
	return quota(s.Store.Usage(), acc, target{community: community, replyTo: replyTo}, s.now())
}

// quota evaluates every limit and rule; the reasons of all blocking ones are
// reported, and NextAt is when the last of the timed ones lifts.
func quota(u store.Usage, acc *store.Account, t target, now time.Time) (Quota, error) {
	l := LimitsOf(acc)
	q := Quota{Limits: l, Daily: l.Daily}
	var b blocks
	b.account(acc, now)
	if err := b.window(u, acc, l, now, &q); err != nil {
		return q, err
	}
	if err := b.community(u, acc, l, t.community, now); err != nil {
		return q, err
	}
	if err := b.thread(u, acc, t); err != nil {
		return q, err
	}
	if len(b.reasons) == 0 {
		q.Ready = true
		return q, nil
	}
	q.Reason = strings.Join(b.reasons, "; ")
	if !b.permanent {
		next := b.next
		q.NextAt = &next
	}
	return q, nil
}

// blocks collects why a publication cannot happen now: next is when the last
// timed block lifts, permanent that one needs a person.
type blocks struct {
	reasons   []string
	next      time.Time
	permanent bool
}

// add records a block lifting at at; a zero at never lifts by itself.
func (b *blocks) add(at time.Time, reason string) {
	b.reasons = append(b.reasons, reason)
	if at.IsZero() {
		b.permanent = true
	} else if at.After(b.next) {
		b.next = at
	}
}

// account adds what the account's own state blocks: health, pause, a
// platform rate limit.
func (b *blocks) account(acc *store.Account, now time.Time) {
	if Dead(acc.Status) {
		r := "account is " + acc.Status
		if acc.StatusDetail != nil && *acc.StatusDetail != "" {
			r += " (" + *acc.StatusDetail + ")"
		}
		b.add(time.Time{}, r+"; reconnect it")
	}
	if acc.Paused {
		b.add(time.Time{}, "account is paused")
	}
	if acc.LimitedUntil != nil {
		if until := store.ParseStamp(*acc.LimitedUntil); until.After(now) {
			b.add(until, "rate-limited by "+acc.Platform)
		}
	}
}

// window adds the daily limit and the minimum interval, and fills in q's
// usage of the rolling 24 hours.
func (b *blocks) window(u store.Usage, acc *store.Account, l publish.Limits, now time.Time, q *Quota) error {
	times, err := u.PublishedTimes(acc.ID, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	q.Used24h = len(times)
	q.Remaining = max(0, l.Daily-q.Used24h)
	if q.Used24h >= l.Daily {
		// The slot frees when enough of the window's publications age out.
		at := now
		if i := q.Used24h - l.Daily; l.Daily > 0 && i < len(times) {
			at = times[i].Add(24 * time.Hour)
		}
		b.add(at, fmt.Sprintf("daily limit reached (%d in 24h)", l.Daily))
	}
	last, err := u.LastPublished(acc.ID)
	if err != nil {
		return err
	}
	if cooling(last, l.MinInterval, now) {
		b.add(last.Add(l.MinInterval), fmt.Sprintf("minimum interval of %s since the last publication", l.MinInterval))
	}
	return nil
}

// community adds the community cooldown, for this account and for the
// owner's other accounts on the platform.
func (b *blocks) community(u store.Usage, acc *store.Account, l publish.Limits, community string, now time.Time) error {
	c := strings.TrimSpace(community)
	if c == "" || l.CommunityCooldown <= 0 {
		return nil
	}
	own, err := u.LastPublishedIn(acc.ID, c)
	if err != nil {
		return err
	}
	if cooling(own, l.CommunityCooldown, now) {
		b.add(own.Add(l.CommunityCooldown), fmt.Sprintf("community cooldown for %s (%s)", c, l.CommunityCooldown))
	}
	other, err := u.LastPublishedInByOthers(acc.Platform, acc.ID, c)
	if err != nil {
		return err
	}
	if cooling(other, l.CommunityCooldown, now) {
		b.add(other.Add(l.CommunityCooldown), fmt.Sprintf("another of our %s accounts published in %s recently", acc.Platform, c))
	}
	return nil
}

// thread adds the rules that keep one owner's accounts apart: one account
// per thread, and nothing removed from one account is re-published by another.
func (b *blocks) thread(u store.Usage, acc *store.Account, t target) error {
	if strings.TrimSpace(t.replyTo) != "" {
		taken, err := u.OtherAccountInThread(acc.Platform, acc.ID, t.replyTo)
		if err != nil {
			return err
		}
		if taken {
			b.add(time.Time{}, "another of our accounts already replied in this thread")
		}
	}
	if strings.TrimSpace(t.replyTo) == "" && strings.TrimSpace(t.title) == "" {
		return nil
	}
	handle, err := u.RemovedFromOtherAccount(acc.Platform, acc.ID, t.replyTo, t.community, t.title)
	if err != nil {
		return err
	}
	if handle != "" {
		b.add(time.Time{}, "the same publication was removed from "+handle+"; it is not re-published from another account")
	}
	return nil
}

// cooling reports whether a wait of d after last is still running at now.
func cooling(last time.Time, d time.Duration, now time.Time) bool {
	return !last.IsZero() && now.Before(last.Add(d))
}

func (q Quota) err() *QuotaError {
	e := &QuotaError{Reason: q.Reason}
	if q.NextAt != nil {
		e.NextAt = *q.NextAt
	}
	return e
}

// PickAccount chooses the account for a draft without one: live or unknown,
// not paused, not limited, within its limits and not breaking the
// one-account-per-thread rule; most remaining quota first, then least
// recently used, then the oldest. The candidates are the draft project's pool
// for the platform, or every account of the owner on it when the pool is
// empty. A draft with an account gets that account, checked the same way.
//
// The draft must be one userID may act on (user 0: the server, acting for
// the draft's owner).
func (s *Service) PickAccount(userID int64, d *store.Draft) (*store.Account, error) {
	u, now, t := s.Store.Usage(), s.now(), targetOf(d)
	if d.AccountID != nil {
		return s.assignedAccount(userID, u, d, t, now)
	}
	accs, err := s.pickCandidates(ownerOf(userID, d), d)
	if err != nil {
		return nil, err
	}
	if len(accs) == 0 {
		return nil, fmt.Errorf("no %s account connected; run radaro connect %s", d.Platform, d.Platform)
	}
	ready, why, earliest, err := candidates(u, accs, t, now)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		return nil, &QuotaError{NextAt: earliest, Reason: fmt.Sprintf("no %s account can publish this now (%s)", d.Platform, strings.Join(why, "; "))}
	}
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].before(ready[j]) })
	return ready[0].acc, nil
}

// ownerOf is whose accounts a draft may use: the acting user, or the draft's
// owner when the server acts (user 0).
func ownerOf(userID int64, d *store.Draft) int64 {
	if userID != 0 {
		return userID
	}
	return d.UserID
}

// pickCandidates is the project's pool for the draft's platform, or every
// account of the owner on it when the pool is empty or there is no project.
func (s *Service) pickCandidates(owner int64, d *store.Draft) ([]*store.Account, error) {
	if d.ProjectID != nil {
		pool, err := s.Store.ProjectPool(owner, *d.ProjectID, d.Platform)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if len(pool) > 0 {
			return pool, nil
		}
	}
	return s.Store.Accounts(owner, d.Platform)
}

// assignedAccount is the draft's own account, if it may publish the draft now.
func (s *Service) assignedAccount(userID int64, u store.Usage, d *store.Draft, t target, now time.Time) (*store.Account, error) {
	acc, err := s.Store.Account(ownerOf(userID, d), *d.AccountID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, fmt.Errorf("account %d no longer exists; edit the draft or reconnect", *d.AccountID)
	}
	if acc.Platform != d.Platform {
		return nil, fmt.Errorf("account %d is not a %s account", acc.ID, d.Platform)
	}
	q, err := quota(u, acc, t, now)
	if err != nil {
		return nil, err
	}
	if !q.Ready {
		return nil, q.err()
	}
	return acc, nil
}

// candidate is an account that may publish now.
type candidate struct {
	acc  *store.Account
	q    Quota
	last time.Time
}

// before orders candidates: most remaining quota, then least recently used.
func (a candidate) before(b candidate) bool {
	if a.q.Remaining != b.q.Remaining {
		return a.q.Remaining > b.q.Remaining
	}
	if !a.last.Equal(b.last) {
		return a.last.Before(b.last)
	}
	return a.acc.ID < b.acc.ID
}

// candidates splits accs into those that may publish t now and the reasons
// of the others, with the earliest time one of those frees up.
func candidates(u store.Usage, accs []*store.Account, t target, now time.Time) (ready []candidate, why []string, earliest time.Time, err error) {
	for _, acc := range accs {
		q, err := quota(u, acc, t, now)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		if !q.Ready {
			why = append(why, acc.Handle+": "+q.Reason)
			if q.NextAt != nil && (earliest.IsZero() || q.NextAt.Before(earliest)) {
				earliest = *q.NextAt
			}
			continue
		}
		last, err := u.LastPublished(acc.ID)
		if err != nil {
			return nil, nil, time.Time{}, err
		}
		ready = append(ready, candidate{acc, q, last})
	}
	return ready, why, earliest, nil
}

// Publish sends one of the user's approved drafts. Only approved drafts are
// published, and only from the draft owner's accounts.
//
// The draft is claimed (publishing) with every limit re-checked in the same
// write transaction before any network call. A rate limit returns the draft
// to approved on the same account; an account error fails it and marks the
// account. A failed or removed draft is never moved to another account.
func (s *Service) Publish(ctx context.Context, userID, draftID int64) (*store.Draft, *store.Account, error) {
	d, err := s.publishable(userID, draftID)
	if err != nil {
		return d, nil, err
	}
	acc, pub, claimed, err := s.claim(userID, d)
	if err != nil {
		return d, acc, err
	}
	res, pubErr := pub.Publish(ctx, PostOf(claimed))
	if pubErr != nil {
		return s.publishFailed(ctx, claimed, acc, pubErr)
	}
	d, err = s.Store.FinishPublish(claimed.ID, res.RemoteID, res.URL, nil)
	if err != nil {
		return claimed, acc, err
	}
	_ = s.Store.LogActivity(d.UserID, "draft.published", d.ID, acc.Platform+" "+acc.Handle+" "+res.URL)
	if acc.Status != store.AccountLive {
		// A publication is proof the credentials work.
		if acc2, err := s.setStatus(ctx, acc, store.AccountLive, "", time.Time{}); err == nil {
			acc = acc2
		}
	}
	return d, acc, nil
}

// publishable loads an approved draft whose post its platform accepts. The
// draft is returned with the error when it exists.
func (s *Service) publishable(userID, draftID int64) (*store.Draft, error) {
	d, err := s.Store.Draft(userID, draftID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("%w: draft %d does not exist", store.ErrNotFound, draftID)
	}
	if d.Status != store.DraftApproved {
		return d, fmt.Errorf("%w: draft %d is not approved (status: %s); approve it first", store.ErrConflict, d.ID, d.Status)
	}
	pl, ok := publish.LookupPlatform(d.Platform)
	if !ok {
		return d, fmt.Errorf("unknown platform %q", d.Platform)
	}
	return d, pl.Validate(PostOf(d))
}

// claim picks the account and claims the draft for it. An auto-picked
// account can be taken by a concurrent publish between the pick and the
// claim; pick once more before giving up.
func (s *Service) claim(userID int64, d *store.Draft) (*store.Account, publish.Publisher, *store.Draft, error) {
	for attempt := 0; ; attempt++ {
		acc, err := s.PickAccount(userID, d)
		if err != nil {
			return nil, nil, nil, err
		}
		pub, err := s.NewPublisher(acc.Platform, acc.Credentials, s.Version)
		if err != nil {
			return acc, nil, nil, err
		}
		claimed, err := s.Store.ClaimForPublish(userID, d.ID, acc.ID, s.guard(acc, targetOf(d)))
		if err == nil {
			return acc, pub, claimed, nil
		}
		var qe *QuotaError
		if !errors.As(err, &qe) || d.AccountID != nil || attempt > 0 {
			return acc, nil, nil, err
		}
	}
}

// publishFailed settles a claimed draft whose publish failed: a rate limit
// returns it to the queue; anything else fails it, and an account error also
// marks the account.
func (s *Service) publishFailed(ctx context.Context, claimed *store.Draft, acc *store.Account, pubErr error) (*store.Draft, *store.Account, error) {
	var uncertain interface{ PublishUncertain() bool }
	if errors.As(pubErr, &uncertain) && uncertain.PublishUncertain() {
		_ = s.Store.LogActivity(claimed.UserID, "draft.publish_uncertain", claimed.ID, pubErr.Error())
		return claimed, acc, pubErr
	}
	status, retry := publish.Classify(pubErr)
	if status == publish.HealthLimited {
		return s.requeue(ctx, claimed, acc, pubErr, retry)
	}
	d, err := s.Store.FinishPublish(claimed.ID, "", "", pubErr)
	if err != nil {
		return claimed, acc, err
	}
	_ = s.Store.LogActivity(d.UserID, "draft.failed", d.ID, pubErr.Error())
	if status == publish.HealthInvalid || status == publish.HealthSuspended {
		if acc2, err := s.setStatus(ctx, acc, status, detailOf(pubErr), time.Time{}); err == nil {
			acc = acc2
		}
	}
	return d, acc, fmt.Errorf("publishing draft %d failed: %w", d.ID, pubErr)
}

// requeue returns a rate-limited draft to approved and limits its account
// until retry has passed (an hour when the platform did not say).
func (s *Service) requeue(ctx context.Context, claimed *store.Draft, acc *store.Account, pubErr error, retry time.Duration) (*store.Draft, *store.Account, error) {
	if retry <= 0 {
		retry = time.Hour
	}
	d, err := s.Store.ReturnToApproved(claimed.ID, "rate limited: "+pubErr.Error())
	if err != nil {
		return claimed, acc, err
	}
	_ = s.Store.LogActivity(d.UserID, "draft.rate_limited", d.ID, fmt.Sprintf("%s %s: back in the queue, retry after %s",
		acc.Platform, acc.Handle, s.now().Add(retry).UTC().Format(time.RFC3339)))
	if acc2, err := s.setStatus(ctx, acc, store.AccountLimited, detailOf(pubErr), s.now().Add(retry)); err == nil {
		acc = acc2
	}
	return d, acc, fmt.Errorf("draft %d is back in the queue: %w", d.ID, pubErr)
}

// guard re-checks every limit and rule inside the claim's transaction.
func (s *Service) guard(acc *store.Account, t target) func(store.Usage) error {
	return func(u store.Usage) error {
		q, err := quota(u, acc, t, s.now())
		if err != nil {
			return err
		}
		if !q.Ready {
			return q.err()
		}
		return nil
	}
}

func detailOf(err error) string {
	var rl *publish.RateLimitError
	if errors.As(err, &rl) && rl.Detail != "" {
		return rl.Detail
	}
	var ae *publish.AccountError
	if errors.As(err, &ae) && ae.Detail != "" {
		return ae.Detail
	}
	return err.Error()
}

// setStatus stores a health result, logs a change to the owner's activity
// and, when the account just went dead, notifies.
func (s *Service) setStatus(ctx context.Context, acc *store.Account, status, detail string, limitedUntil time.Time) (*store.Account, error) {
	changed, err := s.Store.SetAccountStatus(acc.ID, status, detail, limitedUntil)
	if err != nil {
		return acc, err
	}
	if changed {
		line := fmt.Sprintf("%s %s: %s → %s", acc.Platform, acc.Handle, acc.Status, status)
		if detail != "" {
			line += " (" + detail + ")"
		}
		_ = s.Store.LogActivity(acc.UserID, "account.status", 0, line)
		if Dead(status) {
			s.notifyDead(ctx, acc, status, detail)
		}
	}
	updated, err := s.Store.Account(acc.UserID, acc.ID)
	if err != nil || updated == nil {
		return acc, err
	}
	return updated, nil
}

// notifyDead alerts that acc just went invalid or suspended.
func (s *Service) notifyDead(ctx context.Context, acc *store.Account, status, detail string) {
	if s.Notify == nil {
		return
	}
	text := fmt.Sprintf("Radaro: %s account %s is %s", acc.Platform, acc.Handle, status)
	if detail != "" {
		text += ": " + detail
	}
	text += ". It will not publish until reconnected."
	if err := s.Notify(ctx, text, map[string]any{
		"event": "radaro.account_" + status, "account_id": acc.ID, "platform": acc.Platform,
		"handle": acc.Handle, "status": status, "detail": detail,
	}); err != nil {
		s.logf("account alert: %v", err)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Check runs a health check on one of the user's accounts and stores the
// result. If the check itself cannot run (network, 5xx), the stored status
// is left as it was and the error is returned.
func (s *Service) Check(ctx context.Context, userID, accountID int64) (*store.Account, error) {
	acc, err := s.Store.Account(userID, accountID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, fmt.Errorf("%w: account %d does not exist", store.ErrNotFound, accountID)
	}
	return s.check(ctx, acc)
}

func (s *Service) check(ctx context.Context, acc *store.Account) (*store.Account, error) {
	pub, err := s.NewPublisher(acc.Platform, acc.Credentials, s.Version)
	if err != nil {
		return acc, err
	}
	checker, ok := pub.(publish.Checker)
	if !ok {
		return acc, fmt.Errorf("%s accounts cannot be checked", acc.Platform)
	}
	h, err := checker.Check(ctx)
	if err != nil {
		status, retry := publish.Classify(err)
		if status == "" {
			return acc, fmt.Errorf("checking %s %s: %w", acc.Platform, acc.Handle, err)
		}
		h = publish.Health{Status: status, Detail: detailOf(err), RetryAfter: retry}
	}
	var until time.Time
	if h.Status == publish.HealthLimited {
		retry := h.RetryAfter
		if retry <= 0 {
			retry = time.Hour
		}
		until = s.now().Add(retry)
	}
	return s.setStatus(ctx, acc, h.Status, h.Detail, until)
}

// CheckError is a check of one account that could not run.
type CheckError struct {
	AccountID int64
	Err       error
}

func (e *CheckError) Error() string { return e.Err.Error() }
func (e *CheckError) Unwrap() error { return e.Err }

// CheckErrors lists the per-account failures in an error from CheckAll.
func CheckErrors(err error) []*CheckError {
	var list []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		list = j.Unwrap()
	} else if err != nil {
		list = []error{err}
	}
	out := []*CheckError{}
	for _, e := range list {
		var ce *CheckError
		if errors.As(e, &ce) {
			out = append(out, ce)
		}
	}
	return out
}

// CheckAll checks every account of the user (user 0: of every user), one at
// a time. Checks that could not run are joined into the error as
// *CheckError; see CheckErrors.
func (s *Service) CheckAll(ctx context.Context, userID int64) error {
	accs, err := s.Store.Accounts(userID, "")
	if err != nil {
		return err
	}
	var errs []error
	for _, acc := range accs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.check(ctx, acc); err != nil {
			errs = append(errs, &CheckError{AccountID: acc.ID, Err: err})
		}
	}
	return errors.Join(errs...)
}

// RefreshMetrics refreshes engagement of the user's published drafts (user
// 0: every user's) and records removals; it returns per-draft problems that
// did not stop the run.
//
// A publication found removed is marked and its account is paused, so a
// person looks at what happened before it publishes again.
func (s *Service) RefreshMetrics(ctx context.Context, userID int64) ([]string, error) {
	ds, err := s.Store.Drafts(userID, store.DraftPublished, 0)
	if err != nil {
		return nil, err
	}
	var problems []string
	r := &metricsRun{s: s, userID: userID, conns: map[int64]*accountConn{}, byPlatform: map[platformKey][]*store.Account{}}
	for _, d := range ds {
		if ctx.Err() != nil {
			return problems, ctx.Err()
		}
		if d.RemoteID == nil || d.RemovedAt != nil {
			continue
		}
		problem, err := r.refresh(ctx, d)
		if err != nil {
			return problems, err
		}
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems, nil
}

// metricsRun is one RefreshMetrics, caching accounts and connectors so each
// is loaded and built once.
type metricsRun struct {
	s          *Service
	userID     int64
	conns      map[int64]*accountConn
	byPlatform map[platformKey][]*store.Account
}

// platformKey is one owner's accounts on one platform.
type platformKey struct {
	owner    int64
	platform string
}

// refresh refreshes one draft's metrics. A problem that should not stop the
// run is returned as text; err stops it.
func (r *metricsRun) refresh(ctx context.Context, d *store.Draft) (problem string, err error) {
	acc, err := r.account(d)
	if err != nil {
		return "", err
	}
	if acc == nil {
		return fmt.Sprintf("draft %d: its account is no longer connected", d.ID), nil
	}
	c := r.conn(acc)
	if c.err != nil {
		return fmt.Sprintf("draft %d: %v", d.ID, c.err), nil
	}
	m, err := c.pub.Metrics(ctx, *d.RemoteID)
	if err != nil {
		r.s.metricsFailed(ctx, c, err)
		return fmt.Sprintf("draft %d: %v", d.ID, err), nil
	}
	if err := r.s.Store.SaveMetrics(d.ID, m); err != nil {
		return "", err
	}
	if removed, _ := m[publish.MetricRemoved].(bool); removed {
		return "", r.s.removed(c, d)
	}
	return "", nil
}

// account is the account that published d, or nil when it is gone.
func (r *metricsRun) account(d *store.Draft) (*store.Account, error) {
	owner := ownerOf(r.userID, d)
	if d.AccountID != nil {
		if c, ok := r.conns[*d.AccountID]; ok {
			return c.acc, nil
		}
		return r.s.Store.Account(owner, *d.AccountID)
	}
	// Drafts published before accounts were recorded: only an unambiguous
	// account of the draft's owner can be used.
	key := platformKey{owner: owner, platform: d.Platform}
	accs, ok := r.byPlatform[key]
	if !ok {
		var err error
		if accs, err = r.s.Store.Accounts(owner, d.Platform); err != nil {
			return nil, err
		}
		r.byPlatform[key] = accs
	}
	if len(accs) == 1 {
		return accs[0], nil
	}
	return nil, nil
}

func (r *metricsRun) conn(acc *store.Account) *accountConn {
	c, ok := r.conns[acc.ID]
	if !ok {
		c = &accountConn{acc: acc}
		c.pub, c.err = r.s.NewPublisher(acc.Platform, acc.Credentials, r.s.Version)
		r.conns[acc.ID] = c
	}
	return c
}

// metricsFailed stores what a failed metrics call says about the account's
// health, if anything.
func (s *Service) metricsFailed(ctx context.Context, c *accountConn, err error) {
	status, retry := publish.Classify(err)
	if status == "" {
		return
	}
	var until time.Time
	if status == publish.HealthLimited {
		until = s.now().Add(max(retry, time.Minute))
	}
	if updated, err := s.setStatus(ctx, c.acc, status, detailOf(err), until); err == nil {
		c.acc = updated
	}
}

// accountConn is one account's connector, built once per refresh.
type accountConn struct {
	acc *store.Account
	pub publish.Publisher
	err error
}

// AutoPauseNote starts the status detail of an account the outbox paused
// itself; resuming the account clears such a note.
const AutoPauseNote = "paused automatically"

// AutoPaused reports whether acc carries the outbox's own pause note.
func AutoPaused(acc *store.Account) bool {
	return acc.StatusDetail != nil && strings.HasPrefix(*acc.StatusDetail, AutoPauseNote)
}

// removed records a removal and pauses the account that published it.
func (s *Service) removed(c *accountConn, d *store.Draft) error {
	newly, err := s.Store.MarkRemoved(d.ID)
	if err != nil || !newly {
		return err
	}
	_ = s.Store.LogActivity(d.UserID, "draft.removed", d.ID, fmt.Sprintf("%s %s: %s was removed by the platform or moderators",
		c.acc.Platform, c.acc.Handle, orDefault(deref(d.RemoteURL), deref(d.RemoteID))))
	if c.acc.Paused {
		return nil
	}
	paused := true
	reason := fmt.Sprintf(AutoPauseNote+": draft %d was removed by the platform or moderators; review before resuming", d.ID)
	acc, err := s.Store.UpdateAccount(c.acc.UserID, c.acc.ID, store.AccountUpdate{Paused: &paused, StatusDetail: &reason})
	if err != nil {
		return err
	}
	if acc != nil {
		c.acc = acc
	}
	_ = s.Store.LogActivity(c.acc.UserID, "account.paused", d.ID, c.acc.Platform+" "+c.acc.Handle+": "+reason)
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Run calls CheckAll and RefreshMetrics across all users every interval
// until ctx ends. A zero or negative interval disables it.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// runOnce is one round of Run; problems go to Logf.
func (s *Service) runOnce(ctx context.Context) {
	if err := s.CheckAll(ctx, 0); err != nil && ctx.Err() == nil {
		s.logf("account check: %v", err)
	}
	problems, err := s.RefreshMetrics(ctx, 0)
	if err != nil && ctx.Err() == nil {
		s.logf("metrics refresh: %v", err)
	}
	for _, p := range problems {
		s.logf("metrics refresh: %s", p)
	}
}

// PostOf is what a draft asks its connector to publish.
func PostOf(d *store.Draft) publish.Post {
	return publish.Post{Kind: d.Kind, Community: deref(d.Community), Title: deref(d.Title), Body: d.Body,
		ReplyTo: deref(d.ReplyTo), IdempotencyKey: fmt.Sprintf("radaro-draft-%d", d.ID)}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
