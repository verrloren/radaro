package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

// plainPub is a connector without health checks.
type plainPub struct{}

func (plainPub) Publish(context.Context, publish.Post) (publish.Result, error) {
	return publish.Result{}, nil
}
func (plainPub) Metrics(context.Context, string) (publish.Metrics, error) { return nil, nil }

// logs collects what a Service reports through Logf.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestQuotaErrorNamesNextTime(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	e := &QuotaError{NextAt: at, Reason: "daily limit reached (5 in 24h)"}
	want := "daily limit reached (5 in 24h); next possible at " + at.Local().Format("2006-01-02 15:04")
	if e.Error() != want {
		t.Fatalf("%q, want %q", e.Error(), want)
	}
}

func TestServiceWithoutClockUsesWallClock(t *testing.T) {
	h := newHarness(t)
	acc := h.account("bluesky", "a")
	h.publish(store.NewDraft{Platform: "bluesky"})
	s := &Service{Store: h.st}
	q, err := s.Quota(acc, "", "")
	if err != nil || q.Ready || !strings.Contains(q.Reason, "minimum interval") {
		t.Fatalf("a publication just now blocks the interval: %+v %v", q, err)
	}
}

func TestPickAssignedAccount(t *testing.T) {
	h := newHarness(t)
	masto := h.account("mastodon", "m")
	gone := int64(999)
	if _, err := h.s.PickAccount(h.uid, &store.Draft{Platform: "bluesky", AccountID: &gone}); err == nil ||
		!strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("missing account: %v", err)
	}
	if _, err := h.s.PickAccount(h.uid, &store.Draft{Platform: "bluesky", AccountID: &masto.ID}); err == nil ||
		!strings.Contains(err.Error(), "is not a bluesky account") {
		t.Fatalf("other platform: %v", err)
	}
}

func TestPickAccountBreaksTiesByID(t *testing.T) {
	h := newHarness(t)
	first := h.account("bluesky", "first")
	h.account("bluesky", "second")
	d := h.approved(store.NewDraft{Platform: "bluesky"})
	if acc, err := h.s.PickAccount(h.uid, d); err != nil || acc.ID != first.ID {
		t.Fatalf("equal quota and never used: the oldest account, got %+v %v", acc, err)
	}
}

func TestPublishUnknownPlatform(t *testing.T) {
	h := newHarness(t)
	d := h.approved(store.NewDraft{Platform: "friendster"})
	if _, _, err := h.s.Publish(context.Background(), h.uid, d.ID); err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("unknown platform: %v", err)
	}
}

func TestPublishConnectorErrorClaimsNothing(t *testing.T) {
	h := newHarness(t)
	h.account("bluesky", "a")
	h.s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) {
		return nil, errors.New("bad credentials blob")
	}
	d := h.approved(store.NewDraft{Platform: "bluesky"})
	_, acc, err := h.s.Publish(context.Background(), h.uid, d.ID)
	if err == nil || acc == nil {
		t.Fatalf("connector error: %+v %v", acc, err)
	}
	if got, _ := h.st.Draft(h.uid, d.ID); got.Status != store.DraftApproved {
		t.Fatalf("the draft stays approved: %+v", got)
	}
}

func TestRateLimitWithoutRetryWaitsAnHour(t *testing.T) {
	h := newHarness(t)
	h.account("bluesky", "a")
	h.pub("a").publishFn = func(publish.Post) (publish.Result, error) {
		return publish.Result{}, &publish.RateLimitError{}
	}
	d := h.approved(store.NewDraft{Platform: "bluesky"})
	_, acc, err := h.s.Publish(context.Background(), h.uid, d.ID)
	if err == nil || acc.LimitedUntil == nil {
		t.Fatalf("rate limited: %+v %v", acc, err)
	}
	if until := store.ParseStamp(*acc.LimitedUntil); until.Sub(h.now.Add(time.Hour)).Abs() > time.Second {
		t.Fatalf("limited until %v, want an hour from now", until)
	}
}

func TestCheckEdgeCases(t *testing.T) {
	h := newHarness(t)
	var l logs
	h.s.Logf = l.logf
	h.s.Notify = func(context.Context, string, map[string]any) error { return errors.New("webhook down") }
	a := h.account("devto", "a")

	// A refusal without detail is described by the error itself.
	h.pub("a").checkFn = func() (publish.Health, error) {
		return publish.Health{}, &publish.AccountError{Status: publish.HealthInvalid}
	}
	acc, err := h.s.Check(context.Background(), h.uid, a.ID)
	if err != nil || acc.Status != store.AccountInvalid || acc.StatusDetail == nil || *acc.StatusDetail == "" {
		t.Fatalf("invalid %+v %v", acc, err)
	}
	if !strings.Contains(l.String(), "account alert: webhook down") {
		t.Fatalf("a failed alert is logged: %q", l.String())
	}

	h.pub("a").checkFn = func() (publish.Health, error) { return publish.Health{Status: publish.HealthLimited}, nil }
	acc, _ = h.s.Check(context.Background(), h.uid, a.ID)
	if acc.LimitedUntil == nil || store.ParseStamp(*acc.LimitedUntil).Sub(h.now.Add(time.Hour)).Abs() > time.Second {
		t.Fatalf("a limit without a time lasts an hour: %+v", acc)
	}

	h.s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) { return plainPub{}, nil }
	if _, err := h.s.Check(context.Background(), h.uid, a.ID); err == nil || !strings.Contains(err.Error(), "cannot be checked") {
		t.Fatalf("no checker: %v", err)
	}
	h.s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) {
		return nil, errors.New("no connector")
	}
	if _, err := h.s.Check(context.Background(), h.uid, a.ID); err == nil {
		t.Fatal("connector error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.s.CheckAll(ctx, h.uid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestRefreshMetricsWithoutAccount(t *testing.T) {
	h := newHarness(t)
	a := h.account("mastodon", "a")
	d, _ := h.publish(store.NewDraft{Platform: "mastodon"})

	// A draft published before accounts were recorded uses the platform's only account.
	if _, err := h.st.DeleteAccount(h.uid, a.ID); err != nil {
		t.Fatal(err)
	}
	h.account("mastodon", "a")
	if problems, err := h.s.RefreshMetrics(context.Background(), h.uid); err != nil || len(problems) != 0 {
		t.Fatalf("one account: %v %v", problems, err)
	}
	if got, _ := h.st.Draft(h.uid, d.ID); got.AccountID != nil || got.Metrics["score"] != float64(1) {
		t.Fatalf("metrics saved: %+v", got)
	}

	// With two it is ambiguous, and nothing is asked.
	h.account("mastodon", "b")
	problems, err := h.s.RefreshMetrics(context.Background(), h.uid)
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0], "no longer connected") {
		t.Fatalf("ambiguous: %v %v", problems, err)
	}
}

func TestRefreshMetricsProblems(t *testing.T) {
	h := newHarness(t)
	a := h.account("mastodon", "a")
	h.publish(store.NewDraft{Platform: "mastodon", Body: "one"})
	h.now = h.now.Add(time.Hour)
	h.publish(store.NewDraft{Platform: "mastodon", Body: "two"})

	// An error that says nothing about the account is only reported.
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) { return nil, errors.New("HTTP 502") }
	problems, err := h.s.RefreshMetrics(context.Background(), h.uid)
	if err != nil || len(problems) != 2 || h.reload(a).Status != store.AccountLive {
		t.Fatalf("plain errors: %v %v %+v", problems, err, h.reload(a))
	}

	// A rate limit limits the account for at least a minute.
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) {
		return nil, &publish.RateLimitError{RetryAfter: time.Second}
	}
	if _, err := h.s.RefreshMetrics(context.Background(), h.uid); err != nil {
		t.Fatal(err)
	}
	acc := h.reload(a)
	if acc.Status != store.AccountLimited || acc.LimitedUntil == nil ||
		store.ParseStamp(*acc.LimitedUntil).Sub(h.now.Add(time.Minute)).Abs() > time.Second {
		t.Fatalf("limited %+v", acc)
	}

	// A connector that cannot be built is reported once per draft, built once.
	built := 0
	h.s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) {
		built++
		return nil, errors.New("bad credentials blob")
	}
	problems, err = h.s.RefreshMetrics(context.Background(), h.uid)
	if err != nil || len(problems) != 2 || built != 1 || !strings.Contains(problems[0], "bad credentials blob") {
		t.Fatalf("connector: %v %v built %d", problems, err, built)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.s.RefreshMetrics(ctx, h.uid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

// A removal found on an account a person already paused keeps their note.
func TestRemovedOnPausedAccount(t *testing.T) {
	h := newHarness(t)
	a := h.account("devto", "a")
	h.pub("a").publishFn = func(publish.Post) (publish.Result, error) { return publish.Result{RemoteID: "42"}, nil }
	d, _ := h.publish(store.NewDraft{Platform: "devto", Title: "T"})
	yes, note := true, "on holiday"
	_, _ = h.st.UpdateAccount(h.uid, a.ID, store.AccountUpdate{Paused: &yes, StatusDetail: &note})
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) { return publish.Metrics{publish.MetricRemoved: true}, nil }
	if _, err := h.s.RefreshMetrics(context.Background(), h.uid); err != nil {
		t.Fatal(err)
	}
	if acc := h.reload(a); acc.StatusDetail == nil || *acc.StatusDetail != note {
		t.Fatalf("note replaced: %+v", acc)
	}
	acts, _ := h.st.Activities(h.uid, 1)
	if acts[0].Action != "draft.removed" || !strings.Contains(acts[0].Detail, ": 42 was removed") {
		t.Fatalf("a post without a URL is named by its id: %+v", acts[0])
	}
	if got, _ := h.st.Draft(h.uid, d.ID); got.RemovedAt == nil {
		t.Fatal("not marked removed")
	}
}

func TestRunLogsProblems(t *testing.T) {
	h := newHarness(t)
	var l logs
	h.s.Logf = l.logf
	h.account("devto", "a")
	h.publish(store.NewDraft{Platform: "devto", Title: "T"})
	h.pub("a").checkFn = func() (publish.Health, error) { return publish.Health{}, errors.New("HTTP 503") }
	h.pub("a").metricsFn = func(string) (publish.Metrics, error) { return nil, errors.New("HTTP 502") }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.s.Run(ctx, 10*time.Millisecond); close(done) }()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(l.String(), "metrics refresh: draft 1: HTTP 502") {
		select {
		case <-deadline:
			t.Fatalf("logs %q", l.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if !strings.Contains(l.String(), "account check: checking devto a: HTTP 503") {
		t.Fatalf("logs %q", l.String())
	}
}

// failingUsage fails the one query named by fail and answers "never" to the rest.
type failingUsage struct{ fail string }

func (f failingUsage) err(name string) error {
	if name == f.fail {
		return errors.New(name + ": database is locked")
	}
	return nil
}

func (f failingUsage) PublishedSince(int64, time.Time) (int, error) {
	return 0, f.err("PublishedSince")
}
func (f failingUsage) LastPublished(int64) (time.Time, error) {
	return time.Time{}, f.err("LastPublished")
}
func (f failingUsage) LastPublishedIn(int64, string) (time.Time, error) {
	return time.Time{}, f.err("LastPublishedIn")
}
func (f failingUsage) OtherAccountInThread(string, int64, string) (bool, error) {
	return false, f.err("OtherAccountInThread")
}
func (f failingUsage) OtherAccountInCommunity(string, int64, string, time.Time) (bool, error) {
	return false, f.err("OtherAccountInCommunity")
}
func (f failingUsage) PublishedTimes(int64, time.Time) ([]time.Time, error) {
	return nil, f.err("PublishedTimes")
}
func (f failingUsage) LastPublishedInByOthers(string, int64, string) (time.Time, error) {
	return time.Time{}, f.err("LastPublishedInByOthers")
}
func (f failingUsage) RemovedFromOtherAccount(string, int64, string, string, string) (string, error) {
	return "", f.err("RemovedFromOtherAccount")
}

// A query that fails must never let a publication through.
func TestQuotaFailsClosed(t *testing.T) {
	acc := &store.Account{ID: 1, Platform: "reddit", Handle: "a", Status: store.AccountLive}
	all := target{community: "golang", replyTo: "https://reddit.com/r/golang/comments/x", title: "t"}
	if q, err := quota(failingUsage{}, acc, all, time.Now()); err != nil || !q.Ready {
		t.Fatalf("nothing fails: %+v %v", q, err)
	}
	for _, name := range []string{"PublishedTimes", "LastPublished", "LastPublishedIn", "LastPublishedInByOthers",
		"OtherAccountInThread", "RemovedFromOtherAccount"} {
		q, err := quota(failingUsage{fail: name}, acc, all, time.Now())
		if err == nil || q.Ready {
			t.Errorf("%s failing: %+v %v", name, q, err)
		}
	}
}

// Store failures are errors, never a silent pass or an empty answer.
func TestStoreFailuresAreErrors(t *testing.T) {
	h := newHarness(t)
	acc := h.account("bluesky", "a")
	d := h.approved(store.NewDraft{Platform: "bluesky"})
	h.st.Close()
	ctx := context.Background()
	if _, err := h.s.PickAccount(h.uid, d); err == nil {
		t.Error("pick")
	}
	if _, err := h.s.PickAccount(h.uid, &store.Draft{Platform: "bluesky", AccountID: &acc.ID}); err == nil {
		t.Error("pick assigned")
	}
	if _, _, err := h.s.Publish(ctx, h.uid, d.ID); err == nil {
		t.Error("publish")
	}
	if _, err := h.s.Check(ctx, h.uid, acc.ID); err == nil {
		t.Error("check")
	}
	if err := h.s.CheckAll(ctx, h.uid); err == nil {
		t.Error("check all")
	}
	if _, err := h.s.RefreshMetrics(ctx, h.uid); err == nil {
		t.Error("refresh")
	}
}
