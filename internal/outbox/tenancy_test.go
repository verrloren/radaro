package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

const (
	golangThread = "https://www.reddit.com/r/golang/comments/abc/x/"
	secondEmail  = "second@example.com"
)

// Two users with accounts on the same platform: one user's publications
// never count as "another of our accounts" for the other.
func TestUsersDoNotShareRules(t *testing.T) {
	h := newHarness(t)
	b := h.as(h.user(secondEmail))
	a1 := h.account("reddit", "a1")
	b1 := b.account("reddit", "b1")
	h.publish(store.NewDraft{Platform: "reddit", Community: "golang", Title: "Launch", AccountID: a1.ID})
	h.now = h.now.Add(time.Hour)
	h.publish(store.NewDraft{Platform: "reddit", ReplyTo: golangThread, AccountID: a1.ID})
	h.now = h.now.Add(time.Hour)

	for name, n := range map[string]store.NewDraft{
		"community cooldown": {Platform: "reddit", Community: "r/golang", Title: "Other"},
		"one thread":         {Platform: "reddit", ReplyTo: golangThread},
	} {
		d := b.approved(n)
		if acc, err := b.s.PickAccount(b.uid, d); err != nil || acc.ID != b1.ID {
			t.Errorf("%s: B is blocked by A's publications: %+v %v", name, acc, err)
		}
	}
	if q, err := h.s.Quota(b1, "golang", golangThread); err != nil || !q.Ready {
		t.Fatalf("B's quota counts A's publications: %+v %v", q, err)
	}
}

// A post removed from one user's account does not stop another user.
func TestRemovalOfAnotherUserDoesNotBlock(t *testing.T) {
	h := newHarness(t)
	b := h.as(h.user(secondEmail))
	a1 := h.account("reddit", "a1")
	b1 := b.account("reddit", "b1")
	d, _ := h.publish(store.NewDraft{Platform: "reddit", Community: "golang", Title: "Launch", AccountID: a1.ID})
	if _, err := h.st.MarkRemoved(d.ID); err != nil {
		t.Fatal(err)
	}
	same := b.approved(store.NewDraft{Platform: "reddit", Community: "golang", Title: "launch"})
	if acc, err := b.s.PickAccount(b.uid, same); err != nil || acc.ID != b1.ID {
		t.Fatalf("A's removal blocked B: %+v %v", acc, err)
	}
}

// Auto-pick only ever chooses the user's own accounts, and a user cannot act
// on another user's drafts or accounts.
func TestPickAndPublishStayWithTheOwner(t *testing.T) {
	h := newHarness(t)
	b := h.as(h.user(secondEmail))
	h.account("bluesky", "a1") // older and with as much quota: would win a tie
	b1 := b.account("bluesky", "b1")
	d := b.approved(store.NewDraft{Platform: "bluesky"})
	if acc, err := b.s.PickAccount(b.uid, d); err != nil || acc.ID != b1.ID {
		t.Fatalf("picked %+v %v, want B's own account", acc, err)
	}
	ctx := context.Background()
	if _, _, err := h.s.Publish(ctx, h.uid, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("A published B's draft: %v", err)
	}
	if _, err := h.s.Check(ctx, h.uid, b1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("A checked B's account: %v", err)
	}
	pinned := b.approved(store.NewDraft{Platform: "bluesky"})
	a2 := h.account("bluesky", "a2")
	pinned.AccountID = &a2.ID // as if pinned behind the store's back
	if _, err := b.s.PickAccount(b.uid, pinned); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("B used A's account: %v", err)
	}
	// The server (user 0) publishes a draft from its owner's accounts only.
	got, acc, err := h.s.Publish(ctx, 0, d.ID)
	if err != nil || got.Status != store.DraftPublished || acc.ID != b1.ID {
		t.Fatalf("server publish %+v %+v %v", got, acc, err)
	}
	if len(h.pub("a1").posts) != 0 {
		t.Fatal("A's account posted B's draft")
	}
}

// A project's pool is where a draft publishes from; only an empty pool falls
// back to all of the user's accounts on the platform.
func TestPickUsesTheProjectPool(t *testing.T) {
	h := newHarness(t)
	h.account("mastodon", "outside") // older: would win a tie without the pool
	pooled := h.account("mastodon", "pooled")
	p, err := h.st.CreateProject(h.uid, "Launch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.BindAccount(h.uid, p.ID, pooled.ID); err != nil {
		t.Fatal(err)
	}
	d := h.approved(store.NewDraft{Platform: "mastodon", ProjectID: p.ID})
	if acc, err := h.s.PickAccount(h.uid, d); err != nil || acc.ID != pooled.ID {
		t.Fatalf("pool: %+v %v", acc, err)
	}
	yes := true
	if _, err := h.st.UpdateAccount(h.uid, pooled.ID, store.AccountUpdate{Paused: &yes}); err != nil {
		t.Fatal(err)
	}
	var qe *QuotaError
	if _, err := h.s.PickAccount(h.uid, d); !errors.As(err, &qe) || !strings.Contains(qe.Reason, "pooled: account is paused") {
		t.Fatalf("a blocked pool does not fall back to other accounts: %v", err)
	}
	if _, err := h.st.UnbindProjectAccount(h.uid, p.ID, pooled.ID); err != nil {
		t.Fatal(err)
	}
	if acc, err := h.s.PickAccount(h.uid, d); err != nil || acc.Handle != "outside" {
		t.Fatalf("empty pool falls back to the user's accounts: %+v %v", acc, err)
	}
}

// The background run (user 0) checks every user's accounts and logs each
// status change to its owner only.
func TestCheckAllAcrossUsers(t *testing.T) {
	h := newHarness(t)
	b := h.as(h.user(secondEmail))
	a1 := h.account("devto", "a1")
	b1 := b.account("devto", "b1")
	ctx := context.Background()
	if err := h.s.CheckAll(ctx, b.uid); err != nil {
		t.Fatal(err)
	}
	if h.reload(a1).Status != store.AccountUnknown || b.reload(b1).Status != store.AccountLive {
		t.Fatal("CheckAll(user) must check that user's accounts only")
	}
	h.pub("a1").checkFn = func() (publish.Health, error) {
		return publish.Health{Status: publish.HealthSuspended, Detail: "banned"}, nil
	}
	if err := h.s.CheckAll(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if h.reload(a1).Status != store.AccountSuspended {
		t.Fatal("the server run skipped A's account")
	}
	if acts := h.actions(); !strings.Contains(acts, "account.status") {
		t.Fatalf("A's log lacks the status change: %s", acts)
	}
	if acts := b.actions(); strings.Count(acts, "account.status") != 1 {
		t.Fatalf("B's log holds only its own change: %s", acts)
	}
	if len(h.notified) != 1 || h.notified[0]["handle"] != "a1" {
		t.Fatalf("notified %v", h.notified)
	}
}

// RefreshMetrics for one user leaves others alone; the server run pauses the
// owner's account on a removal and never borrows another user's account.
func TestRefreshMetricsAcrossUsers(t *testing.T) {
	h := newHarness(t)
	b := h.as(h.user(secondEmail))
	h.account("devto", "a1")
	b.account("devto", "b1")
	h.pub("a1").publishFn = func(publish.Post) (publish.Result, error) { return publish.Result{RemoteID: "a-post"}, nil }
	h.pub("b1").publishFn = func(publish.Post) (publish.Result, error) { return publish.Result{RemoteID: "b-post"}, nil }
	da, _ := h.publish(store.NewDraft{Platform: "devto", Title: "A"})
	db, _ := b.publish(store.NewDraft{Platform: "devto", Title: "B"})
	h.pub("b1").metricsFn = func(string) (publish.Metrics, error) { return publish.Metrics{publish.MetricRemoved: true}, nil }

	ctx := context.Background()
	if _, err := h.s.RefreshMetrics(ctx, h.uid); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.st.Draft(b.uid, db.ID); got.MetricsAt != nil {
		t.Fatal("A's refresh touched B's draft")
	}
	if problems, err := h.s.RefreshMetrics(ctx, 0); err != nil || len(problems) != 0 {
		t.Fatalf("server run %v %v", problems, err)
	}
	if got, _ := b.st.Draft(b.uid, db.ID); got.RemovedAt == nil {
		t.Fatal("B's removal was not recorded")
	}
	if acts := b.actions(); !strings.Contains(acts, "draft.removed,account.paused") {
		t.Fatalf("B's log %s", acts)
	}
	if got, _ := h.st.Draft(h.uid, da.ID); got.RemovedAt != nil || strings.Contains(h.actions(), "removed") {
		t.Fatal("A was affected by B's removal")
	}
}

// Warnings compare a draft only with the owner's own publications.
func TestWarningsIgnoreOtherUsers(t *testing.T) {
	s, st, uid := warnService(t)
	other, err := st.CreateUser(secondEmail, "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	theirs := account(t, st, other.ID, "mastodon", "them@social")
	mine := account(t, st, uid, "mastodon", "me@social")
	text := "Radaro watches Hacker News, Reddit and Bluesky for mentions of your project and drafts replies"
	published(t, st, store.NewDraft{UserID: other.ID, Platform: "mastodon", AccountID: theirs.ID, Kind: "post", Body: text})
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "mastodon", AccountID: mine.ID, Kind: "post", Body: text})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil || codes(ws)[WarnDuplicate] != 0 {
		t.Fatalf("another user's publication is not ours: %+v %v", ws, err)
	}
}
