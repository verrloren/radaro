package store

import (
	"testing"
	"time"
)

// dayToday is the day the tests' clock stops on.
const dayToday = "2026-09-30"

// draftRow is a draft row with the timestamps a test needs; nil times stay NULL.
type draftRow struct {
	account            int64
	status             string
	updated            time.Time
	published, removed *time.Time
	err                string
}

// insertDraft writes a draft row directly.
func insertDraft(t *testing.T, st *Store, r draftRow) {
	t.Helper()
	opt := func(tm *time.Time) any {
		if tm == nil {
			return nil
		}
		return stamp(*tm)
	}
	if _, err := st.db.Exec(`INSERT INTO drafts (user_id, platform, account_id, kind, body, status, error, created_at, updated_at, published_at, removed_at)
		VALUES ((SELECT user_id FROM accounts WHERE id = ?), 'bluesky', ?, 'post', 'x', ?, ?, ?, ?, ?, ?)`,
		r.account, r.account, r.status, nullIfEmpty(r.err), stamp(r.updated), stamp(r.updated), opt(r.published), opt(r.removed)); err != nil {
		t.Fatal(err)
	}
}

func insertEvent(t *testing.T, st *Store, accountID int64, platform string, at time.Time, status string) {
	t.Helper()
	if _, err := st.db.Exec(`INSERT INTO account_status_events (account_id, platform, at, status) VALUES (?, ?, ?, ?)`,
		accountID, platform, stamp(at), status); err != nil {
		t.Fatal(err)
	}
}

func setCreated(t *testing.T, st *Store, accountID int64, at time.Time) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE accounts SET created_at = ? WHERE id = ?`, stamp(at), accountID); err != nil {
		t.Fatal(err)
	}
}

func TestAccountActivity(t *testing.T) {
	st := openTest(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	owner, stranger := newUser(t, st, emailA), newUser(t, st, emailB)
	a := saveAccount(t, st, owner.ID, platBluesky, "a")
	b := saveAccount(t, st, owner.ID, platBluesky, "b")
	theirs := saveAccount(t, st, stranger.ID, platBluesky, "theirs")
	at := func(d time.Duration) *time.Time { tm := now.Add(-d); return &tm }

	for _, r := range []draftRow{
		{status: DraftPublished, updated: *at(time.Hour), published: at(time.Hour)},                                             // 24h
		{status: DraftPublished, updated: *at(3 * 24 * time.Hour), published: at(3 * 24 * time.Hour), removed: at(time.Hour)},   // 7d, removed
		{status: DraftPublished, updated: *at(20 * 24 * time.Hour), published: at(20 * 24 * time.Hour)},                         // 30d
		{status: DraftPublished, updated: *at(40 * 24 * time.Hour), published: at(40 * 24 * time.Hour), removed: at(time.Hour)}, // too old
		{status: DraftFailed, updated: *at(2 * time.Hour), err: "HTTP 500 older"},
		{status: DraftFailed, updated: *at(time.Hour), err: "HTTP 403 latest"},
		{status: DraftFailed, updated: *at(35 * 24 * time.Hour), err: "ancient"},
		{status: DraftApproved, updated: *at(time.Minute)},
	} {
		r.account = a.ID
		insertDraft(t, st, r)
	}

	insertDraft(t, st, draftRow{account: theirs.ID, status: DraftPublished, updated: *at(time.Hour), published: at(time.Hour)})

	got, err := st.AccountActivity(owner.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[theirs.ID]; ok || len(got) != 2 {
		t.Fatalf("another user's account is listed: %+v", got)
	}
	ra := got[a.ID]
	if ra.Published24h != 1 || ra.Published7d != 2 || ra.Published30d != 3 || ra.Failed30d != 2 || ra.Removed30d != 1 {
		t.Fatalf("counts %+v", ra)
	}
	if ra.LastPublishedAt == nil || *ra.LastPublishedAt != stamp(*at(time.Hour)) {
		t.Fatalf("last published %v", ra.LastPublishedAt)
	}
	if ra.LastError == nil || *ra.LastError != "HTTP 403 latest" {
		t.Fatalf("last error %v", ra.LastError)
	}
	rb, ok := got[b.ID]
	if !ok || rb.AccountID != b.ID || rb.Published30d != 0 || rb.LastPublishedAt != nil || rb.LastError != nil {
		t.Fatalf("idle account %+v %v", rb, ok)
	}
}

func TestBanRateSeries(t *testing.T) {
	st := openTest(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return time.Date(2026, 9, 30-n, 0, 0, 0, 0, time.UTC) }

	owner, stranger := newUser(t, st, emailA), newUser(t, st, emailB)
	r1 := saveAccount(t, st, owner.ID, platReddit, "r1")
	r2 := saveAccount(t, st, owner.ID, platReddit, "r2")
	b1 := saveAccount(t, st, owner.ID, platBluesky, "b1")
	theirs := saveAccount(t, st, stranger.ID, platMastodon, "theirs")
	setCreated(t, st, theirs.ID, day(10))
	insertEvent(t, st, theirs.ID, platMastodon, day(2), AccountSuspended)
	setCreated(t, st, r1.ID, day(10))
	setCreated(t, st, r2.ID, day(10))
	setCreated(t, st, b1.ID, day(1).Add(6*time.Hour)) // exists from yesterday

	insertEvent(t, st, r1.ID, platReddit, day(9), AccountLive) // before the window
	insertEvent(t, st, r1.ID, platReddit, day(2).Add(23*time.Hour), AccountSuspended)
	insertEvent(t, st, r2.ID, platReddit, day(1).Add(time.Hour), AccountInvalid)
	insertEvent(t, st, r2.ID, platReddit, day(0).Add(time.Hour), AccountLive) // came back today
	insertEvent(t, st, b1.ID, platBluesky, day(0).Add(time.Hour), AccountLimited)

	pts, err := st.BanRateSeries(owner.ID, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 6 {
		t.Fatalf("want 3 days × 2 platforms (the user's only), got %d: %+v", len(pts), pts)
	}
	want := []BanRatePoint{
		{"2026-09-28", platBluesky, 0, 0}, {"2026-09-28", platReddit, 2, 1},
		{"2026-09-29", platBluesky, 1, 0}, {"2026-09-29", platReddit, 2, 2},
		{dayToday, platBluesky, 1, 0}, {dayToday, platReddit, 2, 1},
	}
	for i, w := range want {
		if pts[i] != w {
			t.Fatalf("point %d = %+v, want %+v", i, pts[i], w)
		}
	}

	if pts, _ = st.BanRateSeries(owner.ID, 1, now); len(pts) != 2 || pts[0].Day != dayToday {
		t.Fatalf("one day %+v", pts)
	}
	empty := openTest(t)
	if pts, err = empty.BanRateSeries(0, 30, now); err != nil || len(pts) != 0 {
		t.Fatalf("no accounts %+v %v", pts, err)
	}
	if pts, err = st.BanRateSeries(owner.ID, 0, now); err != nil || len(pts) != 0 {
		t.Fatalf("no days %+v %v", pts, err)
	}
	if pts, _ = st.BanRateSeries(stranger.ID, 1, now); len(pts) != 1 || pts[0] != (BanRatePoint{dayToday, platMastodon, 1, 1}) {
		t.Fatalf("the other user's series %+v", pts)
	}
}
