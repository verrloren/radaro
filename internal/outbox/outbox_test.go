package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/store"
)

// fakePub is one account's connector; tests script its answers.
type fakePub struct {
	mu        sync.Mutex
	posts     []publish.Post
	publishFn func(publish.Post) (publish.Result, error)
	metricsFn func(string) (publish.Metrics, error)
	checkFn   func() (publish.Health, error)
}

func TestUncertainBrowserPublishStaysClaimed(t *testing.T) {
	h := newHarness(t)
	h.account("reddit", "alice")
	h.pub("alice").publishFn = func(publish.Post) (publish.Result, error) { return publish.Result{}, &redditbrowser.UncertainError{} }
	d := h.approved(store.NewDraft{Platform: "reddit", Community: "golang", Title: "Hello", Body: "A useful post"})
	got, _, err := h.s.Publish(context.Background(), h.uid, d.ID)
	if err == nil || got.Status != "publishing" {
		t.Fatal("uncertain submission became retryable")
	}
	if _, _, err = h.s.Publish(context.Background(), h.uid, d.ID); err == nil {
		t.Fatal("uncertain submission was sent twice")
	}
	if len(h.pub("alice").posts) != 1 {
		t.Fatal("connector was called twice")
	}
}

func (f *fakePub) Publish(_ context.Context, p publish.Post) (publish.Result, error) {
	f.mu.Lock()
	f.posts = append(f.posts, p)
	n := len(f.posts)
	f.mu.Unlock()
	if f.publishFn != nil {
		return f.publishFn(p)
	}
	return publish.Result{RemoteID: "r" + p.IdempotencyKey, URL: "https://example.test/" + p.IdempotencyKey + "/" + string(rune('0'+n))}, nil
}

func (f *fakePub) Metrics(_ context.Context, id string) (publish.Metrics, error) {
	if f.metricsFn != nil {
		return f.metricsFn(id)
	}
	return publish.Metrics{"score": 1}, nil
}

func (f *fakePub) Check(context.Context) (publish.Health, error) {
	if f.checkFn != nil {
		return f.checkFn()
	}
	return publish.Health{Status: publish.HealthLive}, nil
}

type harness struct {
	t        *testing.T
	st       *store.Store
	s        *Service
	pubs     map[string]*fakePub // by handle
	notified []map[string]any
	now      time.Time
	uid      int64 // the user the helpers act as
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{t: t, st: st, pubs: map[string]*fakePub{}, now: time.Now()}
	h.uid = h.user("owner@example.com")
	h.s = New(st, "test")
	h.s.Now = func() time.Time { return h.now }
	h.s.NewPublisher = func(platform string, creds json.RawMessage, _ string) (publish.Publisher, error) {
		var c struct{ Handle string }
		if err := json.Unmarshal(creds, &c); err != nil {
			return nil, err
		}
		return h.pub(c.Handle), nil
	}
	h.s.Notify = func(_ context.Context, text string, payload map[string]any) error {
		payload["text"] = text
		h.notified = append(h.notified, payload)
		return nil
	}
	return h
}

// user registers a user and returns its id.
func (h *harness) user(email string) int64 {
	h.t.Helper()
	u, err := h.st.CreateUser(email, "hash", true)
	if err != nil {
		h.t.Fatal(err)
	}
	return u.ID
}

// as is h acting as another user; it shares h's store, clock and connectors.
func (h *harness) as(uid int64) *harness {
	c := *h
	c.uid = uid
	return &c
}

func (h *harness) pub(handle string) *fakePub {
	if h.pubs[handle] == nil {
		h.pubs[handle] = &fakePub{}
	}
	return h.pubs[handle]
}

func (h *harness) account(platform, handle string) *store.Account {
	h.t.Helper()
	acc, err := h.st.SaveAccount(h.uid, platform, handle, map[string]string{"handle": handle})
	if err != nil {
		h.t.Fatal(err)
	}
	return acc
}

func (h *harness) approved(n store.NewDraft) *store.Draft {
	h.t.Helper()
	if n.Kind == "" {
		n.Kind = "post"
		if n.ReplyTo != "" {
			n.Kind = "reply"
		}
	}
	if n.Body == "" {
		n.Body = "hello"
	}
	if n.Platform == "reddit" && n.Kind == "post" && n.Title == "" {
		n.Title = "a title"
	}
	n.UserID = h.uid
	d, err := h.st.CreateDraft(n)
	if err != nil {
		h.t.Fatal(err)
	}
	if d, err = h.st.ApproveDraft(h.uid, d.ID); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) publish(n store.NewDraft) (*store.Draft, *store.Account) {
	h.t.Helper()
	d, acc, err := h.s.Publish(context.Background(), h.uid, h.approved(n).ID)
	if err != nil {
		h.t.Fatalf("publish: %v", err)
	}
	return d, acc
}

func (h *harness) reload(acc *store.Account) *store.Account {
	h.t.Helper()
	a, err := h.st.Account(h.uid, acc.ID)
	if err != nil || a == nil {
		h.t.Fatalf("account %d: %v", acc.ID, err)
	}
	return a
}

func (h *harness) actions() string {
	acts, _ := h.st.Activities(h.uid, 100)
	var out []string
	for i := len(acts) - 1; i >= 0; i-- {
		out = append(out, acts[i].Action)
	}
	return strings.Join(out, ",")
}

func set(v int) *int { return &v }

func TestQuotaReasons(t *testing.T) {
	h := newHarness(t)
	acc := h.account("reddit", "a")
	q, err := h.s.Quota(acc, "golang", "")
	if err != nil || !q.Ready || q.Daily != 5 || q.Remaining != 5 || q.NextAt != nil || q.Reason != "" {
		t.Fatalf("fresh quota %+v %v", q, err)
	}
	d, _ := h.publish(store.NewDraft{Platform: "reddit", Community: "r/golang"})
	published := store.ParseStamp(*d.PublishedAt)

	acc = h.reload(acc)
	q, _ = h.s.Quota(acc, "golang", "")
	if q.Ready || q.Used24h != 1 || q.Remaining != 4 || !strings.Contains(q.Reason, "minimum interval") ||
		!strings.Contains(q.Reason, "community cooldown") {
		t.Fatalf("after one post %+v", q)
	}
	if want := published.Add(24 * time.Hour); q.NextAt == nil || !q.NextAt.Equal(want) {
		t.Fatalf("next_at is when the last block lifts: %v, want %v", q.NextAt, want)
	}
	if q, _ = h.s.Quota(acc, "rust", ""); q.Ready || strings.Contains(q.Reason, "cooldown") {
		t.Fatalf("another community only waits for the interval: %+v", q)
	}
	h.now = h.now.Add(11 * time.Minute)
	if q, _ = h.s.Quota(acc, "rust", ""); !q.Ready {
		t.Fatalf("interval passed: %+v", q)
	}
}

func TestQuotaDailyLimit(t *testing.T) {
	h := newHarness(t)
	h.account("reddit", "a")
	d, acc := h.publish(store.NewDraft{Platform: "reddit", Community: "r/golang"})
	published := store.ParseStamp(*d.PublishedAt)
	h.now = h.now.Add(11 * time.Minute)

	acc, _ = h.st.UpdateAccount(h.uid, acc.ID, store.AccountUpdate{DailyLimit: set(1)})
	q, _ := h.s.Quota(acc, "", "")
	if q.Ready || q.Daily != 1 || q.Remaining != 0 || !strings.Contains(q.Reason, "daily limit") ||
		q.NextAt == nil || !q.NextAt.Equal(published.Add(24*time.Hour)) {
		t.Fatalf("daily limit %+v", q)
	}
	b, _ := json.Marshal(q)
	if !strings.Contains(string(b), `"next_at":"`) || !strings.Contains(string(b), `"ready":false`) || strings.Contains(string(b), "Limits") {
		t.Fatalf("quota JSON %s", b)
	}
}

// TestQuotaAccountState covers the blocks that come from the account itself:
// a pause and a dead account need a person, a rate limit lifts by itself.
func TestQuotaAccountState(t *testing.T) {
	h := newHarness(t)
	acc := h.account("reddit", "a")
	var q Quota
	yes := true
	acc, _ = h.st.UpdateAccount(h.uid, acc.ID, store.AccountUpdate{Paused: &yes, DailyLimit: set(0)})
	if q, _ = h.s.Quota(acc, "", ""); q.Ready || q.NextAt != nil || !strings.Contains(q.Reason, "paused") {
		t.Fatalf("paused has no time to wait for: %+v", q)
	}
	no := false
	acc, _ = h.st.UpdateAccount(h.uid, acc.ID, store.AccountUpdate{Paused: &no})
	_, _ = h.st.SetAccountStatus(acc.ID, store.AccountLimited, "429", h.now.Add(time.Hour))
	acc = h.reload(acc)
	if q, _ = h.s.Quota(acc, "", ""); q.Ready || !strings.Contains(q.Reason, "rate-limited") || q.NextAt == nil {
		t.Fatalf("limited %+v", q)
	}
	_, _ = h.st.SetAccountStatus(acc.ID, store.AccountSuspended, "banned", time.Time{})
	acc = h.reload(acc)
	if q, _ = h.s.Quota(acc, "", ""); q.Ready || q.NextAt != nil || !strings.Contains(q.Reason, "suspended (banned)") {
		t.Fatalf("suspended %+v", q)
	}
	if e := (&QuotaError{Reason: "account is paused"}); e.Error() != "account is paused" {
		t.Fatalf("permanent quota error %q", e.Error())
	}
}

func TestPickAccountOrdering(t *testing.T) {
	h := newHarness(t)
	paused := h.account("bluesky", "paused")
	dead := h.account("bluesky", "dead")
	limited := h.account("bluesky", "limited")
	used := h.account("bluesky", "used")
	fresh := h.account("bluesky", "fresh")
	yes := true
	_, _ = h.st.UpdateAccount(h.uid, paused.ID, store.AccountUpdate{Paused: &yes})
	_, _ = h.st.SetAccountStatus(dead.ID, store.AccountInvalid, "401", time.Time{})
	_, _ = h.st.SetAccountStatus(limited.ID, store.AccountLimited, "429", h.now.Add(5*time.Hour))
	_, _ = h.st.SetAccountStatus(used.ID, store.AccountLive, "", time.Time{})

	pin := func(acc *store.Account) {
		_, _, err := h.s.Publish(context.Background(), h.uid, h.approved(store.NewDraft{Platform: "bluesky", AccountID: acc.ID}).ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	pin(used)
	h.now = h.now.Add(time.Hour) // past every interval

	d := h.approved(store.NewDraft{Platform: "bluesky"})
	acc, err := h.s.PickAccount(h.uid, d)
	if err != nil || acc.ID != fresh.ID {
		t.Fatalf("most remaining quota first: %+v %v", acc, err)
	}
	pin(fresh)
	h.now = h.now.Add(time.Hour)
	if acc, _ = h.s.PickAccount(h.uid, d); acc.ID != used.ID {
		t.Fatalf("equal quota: least recently used first, got %s", acc.Handle)
	}
	_, _ = h.st.SetAccountStatus(limited.ID, store.AccountLimited, "429", h.now.Add(-time.Minute))
	if acc, _ = h.s.PickAccount(h.uid, d); acc.ID != limited.ID {
		t.Fatalf("an expired rate limit is no limit; limited has the most quota, got %s", acc.Handle)
	}

	for _, a := range []*store.Account{used, fresh, limited} {
		_, _ = h.st.UpdateAccount(h.uid, a.ID, store.AccountUpdate{Paused: &yes})
	}
	_, err = h.s.PickAccount(h.uid, d)
	var qe *QuotaError
	if !errors.As(err, &qe) || !qe.NextAt.IsZero() || !strings.Contains(qe.Reason, "dead: account is invalid") {
		t.Fatalf("no account: %v", err)
	}
	if _, err := h.s.PickAccount(h.uid, &store.Draft{Platform: "devto"}); err == nil || !strings.Contains(err.Error(), "radaro connect devto") {
		t.Fatalf("no devto account: %v", err)
	}
	pinned := &store.Draft{Platform: "bluesky", AccountID: &dead.ID}
	if _, err := h.s.PickAccount(h.uid, pinned); !errors.As(err, &qe) {
		t.Fatalf("a draft's own account is checked too: %v", err)
	}
}

func TestOneThreadOneAccount(t *testing.T) {
	h := newHarness(t)
	a := h.account("reddit", "a")
	b := h.account("reddit", "b")
	thread := "https://www.reddit.com/r/golang/comments/abc/x/"
	_, first := h.publish(store.NewDraft{Platform: "reddit", ReplyTo: thread, AccountID: a.ID})
	if first.ID != a.ID {
		t.Fatal("pinned account")
	}
	h.now = h.now.Add(time.Hour)

	// b has more quota left, but a is the one already in the thread.
	d := h.approved(store.NewDraft{Platform: "reddit", ReplyTo: strings.TrimSuffix(thread, "/")})
	if acc, err := h.s.PickAccount(h.uid, d); err != nil || acc.ID != a.ID {
		t.Fatalf("thread keeps its account: %+v %v", acc, err)
	}
	onB := h.approved(store.NewDraft{Platform: "reddit", ReplyTo: thread, AccountID: b.ID})
	_, _, err := h.s.Publish(context.Background(), h.uid, onB.ID)
	var qe *QuotaError
	if !errors.As(err, &qe) || !strings.Contains(qe.Reason, "already replied in this thread") {
		t.Fatalf("second account in a thread: %v", err)
	}
	if got, _ := h.st.Draft(h.uid, onB.ID); got.Status != store.DraftApproved || len(h.pub("b").posts) != 0 {
		t.Fatalf("a refused publish sends nothing: %+v", got)
	}
	yes := true
	_, _ = h.st.UpdateAccount(h.uid, a.ID, store.AccountUpdate{Paused: &yes})
	if _, err := h.s.PickAccount(h.uid, d); !errors.As(err, &qe) {
		t.Fatalf("with a paused, nobody may answer: %v", err)
	}
}

func TestCommunityCooldownAcrossAccounts(t *testing.T) {
	h := newHarness(t)
	a := h.account("reddit", "a")
	b := h.account("reddit", "b")
	h.publish(store.NewDraft{Platform: "reddit", Community: "golang", AccountID: a.ID})
	h.now = h.now.Add(time.Hour)

	d := h.approved(store.NewDraft{Platform: "reddit", Community: "r/GoLang", Title: "second"})
	_, err := h.s.PickAccount(h.uid, d)
	var qe *QuotaError
	if !errors.As(err, &qe) || qe.NextAt.IsZero() || !strings.Contains(qe.Reason, "another of our reddit accounts") {
		t.Fatalf("b must wait for the community cooldown too: %v", err)
	}
	other := h.approved(store.NewDraft{Platform: "reddit", Community: "rust"})
	if acc, err := h.s.PickAccount(h.uid, other); err != nil || acc.ID != b.ID {
		t.Fatalf("another community is free: %+v %v", acc, err)
	}
	h.now = h.now.Add(24 * time.Hour)
	if _, err := h.s.PickAccount(h.uid, d); err != nil {
		t.Fatalf("cooldown over: %v", err)
	}
}

func TestRateLimitReturnsDraftAndLimitsAccount(t *testing.T) {
	h := newHarness(t)
	acc := h.account("mastodon", "a")
	h.pub("a").publishFn = func(publish.Post) (publish.Result, error) {
		return publish.Result{}, &publish.RateLimitError{RetryAfter: 30 * time.Minute, Detail: "HTTP 429"}
	}
	d := h.approved(store.NewDraft{Platform: "mastodon"})
	got, gotAcc, err := h.s.Publish(context.Background(), h.uid, d.ID)
	var rl *publish.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("error %v", err)
	}
	if got.Status != store.DraftApproved || got.AccountID == nil || *got.AccountID != acc.ID || got.Error == nil {
		t.Fatalf("draft back in the queue on its account: %+v", got)
	}
	if gotAcc.Status != store.AccountLimited || gotAcc.LimitedUntil == nil {
		t.Fatalf("account %+v", gotAcc)
	}
	if until := store.ParseStamp(*gotAcc.LimitedUntil); until.Sub(h.now.Add(30*time.Minute)).Abs() > time.Second {
		t.Fatalf("limited until %v", until)
	}
	if a := h.actions(); a != "account.status,draft.rate_limited" && a != "draft.rate_limited,account.status" {
		t.Fatalf("activity %s", a)
	}
	var qe *QuotaError
	if _, _, err := h.s.Publish(context.Background(), h.uid, d.ID); !errors.As(err, &qe) || qe.NextAt.IsZero() {
		t.Fatalf("retry before the limit lifts: %v", err)
	}
	h.pub("a").publishFn = nil
	h.now = h.now.Add(31 * time.Minute)
	got, gotAcc, err = h.s.Publish(context.Background(), h.uid, d.ID)
	if err != nil || got.Status != store.DraftPublished || gotAcc.Status != store.AccountLive || gotAcc.LimitedUntil != nil {
		t.Fatalf("after the limit %+v %+v %v", got, gotAcc, err)
	}
	if len(h.notified) != 0 {
		t.Fatal("a rate limit is not an alert")
	}
}

func TestAccountErrorFailsDraftAndMarksAccount(t *testing.T) {
	h := newHarness(t)
	acc := h.account("bluesky", "a")
	h.account("bluesky", "b")
	h.pub("a").publishFn = func(publish.Post) (publish.Result, error) {
		return publish.Result{}, &publish.AccountError{Status: publish.HealthSuspended, Detail: "account takendown"}
	}
	d := h.approved(store.NewDraft{Platform: "bluesky", AccountID: acc.ID})
	got, gotAcc, err := h.s.Publish(context.Background(), h.uid, d.ID)
	if err == nil || got.Status != store.DraftFailed || gotAcc.Status != store.AccountSuspended {
		t.Fatalf("%+v %+v %v", got, gotAcc, err)
	}
	if len(h.notified) != 1 || h.notified[0]["status"] != "suspended" || h.notified[0]["event"] != "radaro.account_suspended" ||
		!strings.Contains(h.notified[0]["text"].(string), "bluesky account a is suspended") {
		t.Fatalf("notify %v", h.notified)
	}
	if !strings.Contains(h.actions(), "draft.failed") || !strings.Contains(h.actions(), "account.status") {
		t.Fatalf("activity %s", h.actions())
	}
	// A failed draft stays on its account: re-approving does not move it.
	_, _ = h.st.ApproveDraft(h.uid, d.ID)
	if _, _, err := h.s.Publish(context.Background(), h.uid, d.ID); err == nil || len(h.pub("b").posts) != 0 {
		t.Fatalf("a failed draft must not move to another account: %v", err)
	}
	// New drafts go to the healthy account.
	fresh := h.approved(store.NewDraft{Platform: "bluesky"})
	if picked, err := h.s.PickAccount(h.uid, fresh); err != nil || picked.Handle != "b" {
		t.Fatalf("pick skips suspended: %+v %v", picked, err)
	}
	// Other errors fail the draft and leave the account alone.
	h.pub("b").publishFn = func(publish.Post) (publish.Result, error) { return publish.Result{}, &publish.APIError{Status: 500} }
	got, gotAcc, _ = h.s.Publish(context.Background(), h.uid, fresh.ID)
	if got.Status != store.DraftFailed || gotAcc.Status != store.AccountUnknown {
		t.Fatalf("plain error %+v %+v", got, gotAcc)
	}
}

func TestRemovedPostPausesAccount(t *testing.T) {
	h := newHarness(t)
	a := h.account("reddit", "a")
	b := h.account("reddit", "b")
	d, _ := h.publish(store.NewDraft{Platform: "reddit", Community: "golang", Title: "Launch", AccountID: a.ID})
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) {
		return publish.Metrics{"score": 0, publish.MetricRemoved: true}, nil
	}
	problems, err := h.s.RefreshMetrics(context.Background(), h.uid)
	if err != nil || len(problems) != 0 {
		t.Fatalf("%v %v", problems, err)
	}
	got, _ := h.st.Draft(h.uid, d.ID)
	if got.RemovedAt == nil || got.Metrics[publish.MetricRemoved] != true {
		t.Fatalf("draft %+v", got)
	}
	acc := h.reload(a)
	if !acc.Paused || acc.StatusDetail == nil || !strings.Contains(*acc.StatusDetail, "was removed") {
		t.Fatalf("account %+v", acc)
	}
	if acts := h.actions(); !strings.Contains(acts, "draft.removed,account.paused") {
		t.Fatalf("activity %s", acts)
	}
	calls := 0
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) { calls++; return publish.Metrics{}, nil }
	if _, err := h.s.RefreshMetrics(context.Background(), h.uid); err != nil || calls != 0 {
		t.Fatalf("removed drafts are not refreshed again: %d %v", calls, err)
	}

	// The same post is not re-published from another account, even after the cooldown.
	h.now = h.now.Add(48 * time.Hour)
	again := h.approved(store.NewDraft{Platform: "reddit", Community: "r/golang", Title: "launch"})
	_, _, err = h.s.Publish(context.Background(), h.uid, again.ID)
	var qe *QuotaError
	if !errors.As(err, &qe) || !strings.Contains(qe.Reason, "removed from a") || len(h.pub("b").posts) != 0 {
		t.Fatalf("re-publish from b: %v", err)
	}
	fresh := h.approved(store.NewDraft{Platform: "reddit", Community: "golang", Title: "something new"})
	if acc, err := h.s.PickAccount(h.uid, fresh); err != nil || acc.ID != b.ID {
		t.Fatalf("new posts still go to b: %+v %v", acc, err)
	}
}

func TestRefreshMetricsClassifiesErrors(t *testing.T) {
	h := newHarness(t)
	a := h.account("mastodon", "a")
	h.publish(store.NewDraft{Platform: "mastodon"})
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) {
		return nil, &publish.AccountError{Status: publish.HealthInvalid, Detail: "token revoked"}
	}
	problems, err := h.s.RefreshMetrics(context.Background(), h.uid)
	if err != nil || len(problems) != 1 {
		t.Fatalf("%v %v", problems, err)
	}
	if acc := h.reload(a); acc.Status != store.AccountInvalid || len(h.notified) != 1 {
		t.Fatalf("account %+v, notified %v", acc, h.notified)
	}
}

func TestCheck(t *testing.T) {
	h := newHarness(t)
	a := h.account("devto", "a")
	h.pub("a").checkFn = func() (publish.Health, error) { return publish.Health{}, errors.New("dial tcp: timeout") }
	acc, err := h.s.Check(context.Background(), h.uid, a.ID)
	if err == nil || acc.Status != store.AccountUnknown || acc.CheckedAt != nil {
		t.Fatalf("a check that could not run changes nothing: %+v %v", acc, err)
	}
	h.pub("a").checkFn = func() (publish.Health, error) { return publish.Health{Status: publish.HealthLive, Detail: "ok"}, nil }
	if acc, err = h.s.Check(context.Background(), h.uid, a.ID); err != nil || acc.Status != store.AccountLive || acc.CheckedAt == nil {
		t.Fatalf("live %+v %v", acc, err)
	}
	h.pub("a").checkFn = func() (publish.Health, error) {
		return publish.Health{Status: publish.HealthLimited, RetryAfter: time.Minute}, nil
	}
	if acc, _ = h.s.Check(context.Background(), h.uid, a.ID); acc.Status != store.AccountLimited || acc.LimitedUntil == nil {
		t.Fatalf("limited %+v", acc)
	}
	h.pub("a").checkFn = func() (publish.Health, error) {
		return publish.Health{Status: publish.HealthInvalid, Detail: "401"}, nil
	}
	if acc, _ = h.s.Check(context.Background(), h.uid, a.ID); acc.Status != store.AccountInvalid || len(h.notified) != 1 {
		t.Fatalf("invalid %+v %v", acc, h.notified)
	}
	if _, _ = h.s.Check(context.Background(), h.uid, a.ID); len(h.notified) != 1 {
		t.Fatal("notify once per change")
	}
	if _, err := h.s.Check(context.Background(), h.uid, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	h.account("devto", "b")
	h.pub("b").checkFn = func() (publish.Health, error) { return publish.Health{}, errors.New("HTTP 503") }
	if err := h.s.CheckAll(context.Background(), h.uid); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("check all reports failures: %v", err)
	}
}

func TestRunChecksUntilCancelled(t *testing.T) {
	h := newHarness(t)
	a := h.account("devto", "a")
	ctx, cancel := context.WithCancel(context.Background())
	checks := make(chan struct{}, 10)
	h.pub("a").checkFn = func() (publish.Health, error) {
		checks <- struct{}{}
		return publish.Health{Status: publish.HealthLive}, nil
	}
	done := make(chan struct{})
	go func() { h.s.Run(ctx, time.Hour); close(done) }()
	select {
	case <-checks:
	case <-time.After(5 * time.Second):
		t.Fatal("Run must check immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run must stop with its context")
	}
	if acc := h.reload(a); acc.Status != store.AccountLive {
		t.Fatalf("status %s", acc.Status)
	}
	h.s.Run(context.Background(), 0) // disabled: returns at once
}

// Concurrent publishes through one service never pass a limit together.
func TestConcurrentPublishRespectsDailyLimit(t *testing.T) {
	h := newHarness(t)
	acc := h.account("bluesky", "a")
	_, _ = h.st.UpdateAccount(h.uid, acc.ID, store.AccountUpdate{DailyLimit: set(1)})
	ids := []int64{h.approved(store.NewDraft{Platform: "bluesky", Body: "1"}).ID, h.approved(store.NewDraft{Platform: "bluesky", Body: "2"}).ID}
	var wg sync.WaitGroup
	errs := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = h.s.Publish(context.Background(), h.uid, id)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		var qe *QuotaError
		if err == nil {
			ok++
		} else if !errors.As(err, &qe) {
			t.Fatalf("unexpected %v", err)
		}
	}
	if ok != 1 || len(h.pub("a").posts) != 1 {
		t.Fatalf("%d publishes passed a limit of 1 (%d sent)", ok, len(h.pub("a").posts))
	}
}

func TestPublishRefusesUnapprovedAndInvalid(t *testing.T) {
	h := newHarness(t)
	h.account("reddit", "a")
	d, _ := h.st.CreateDraft(store.NewDraft{UserID: h.uid, Platform: "reddit", Kind: "post", Community: "golang", Title: "t", Body: "x"})
	if _, _, err := h.s.Publish(context.Background(), h.uid, d.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unapproved: %v", err)
	}
	if _, _, err := h.s.Publish(context.Background(), h.uid, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if len(h.pub("a").posts) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestViewAndLimits(t *testing.T) {
	h := newHarness(t)
	acc := h.account("reddit", "a")
	v, err := h.s.View(acc)
	if err != nil || v.Limits.Daily != 5 || v.Limits.MinIntervalSec != 600 || v.Limits.CommunityCooldownH != 24 || v.Limits.Custom {
		t.Fatalf("defaults %+v %v", v, err)
	}
	acc, _ = h.st.UpdateAccount(h.uid, acc.ID, store.AccountUpdate{DailyLimit: set(2), MinIntervalSec: set(60), CommunityCooldownH: set(48)})
	l := LimitsOf(acc)
	if l.Daily != 2 || l.MinInterval != time.Minute || l.CommunityCooldown != 48*time.Hour {
		t.Fatalf("overrides %+v", l)
	}
	b, _ := json.Marshal(AccountView{Account: acc, Limits: ViewLimits(acc)})
	for _, want := range []string{`"handle":"a"`, `"status":"unknown"`, `"limits":{"daily":2`, `"quota":{`, `"custom":true`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("%s missing from %s", want, b)
		}
	}
	if strings.Contains(string(b), "credentials") || strings.Contains(string(b), `"handle":"a"}`) {
		t.Fatalf("credentials leaked: %s", b)
	}
}
