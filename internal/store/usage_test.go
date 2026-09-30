package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Platforms and communities the account tests publish to.
const (
	platReddit   = "reddit"
	platBluesky  = "bluesky"
	platDevto    = "devto"
	platMastodon = "mastodon"
	subGolang    = "golang"
	subRGolang   = "r/golang"
	subRust      = "rust"
	threadURL    = "https://r/t/"
	threadBare   = "https://r/t"
	someURL      = "https://x"
	titleLaunch  = "Launch"
	kindReply    = "reply"
	kindPost     = "post"
	emailA       = "a@example.com"
	emailB       = "b@example.com"
)

func newUser(t *testing.T, st *Store, email string) *User {
	t.Helper()
	u, err := st.CreateUser(email, "h", true)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func saveAccount(t *testing.T, st *Store, userID int64, platform, handle string) *Account {
	t.Helper()
	acc, err := st.SaveAccount(userID, platform, handle, map[string]string{"k": handle})
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func approvedDraft(t *testing.T, st *Store, n NewDraft) *Draft {
	t.Helper()
	if n.Kind == "" {
		n.Kind = kindPost
	}
	if n.Body == "" {
		n.Body = "hello"
	}
	d, err := st.CreateDraft(n)
	if err != nil {
		t.Fatal(err)
	}
	if d, err = st.ApproveDraft(n.UserID, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func publishDraft(t *testing.T, st *Store, n NewDraft, accountID int64, url string) *Draft {
	t.Helper()
	d := approvedDraft(t, st, n)
	if _, err := st.ClaimForPublish(n.UserID, d.ID, accountID, nil); err != nil {
		t.Fatal(err)
	}
	d, err := st.FinishPublish(d.ID, "id-"+url, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestUpdateAccount(t *testing.T) {
	st := openTest(t)
	u := newUser(t, st, emailA)
	acc := saveAccount(t, st, u.ID, platReddit, "me")
	if acc.Status != AccountUnknown || acc.Paused || acc.DailyLimit != nil {
		t.Fatalf("new account %+v", acc)
	}
	yes, three, ten := true, 3, 600
	acc, err := st.UpdateAccount(u.ID, acc.ID, AccountUpdate{Paused: &yes, DailyLimit: &three, MinIntervalSec: &ten})
	if err != nil || !acc.Paused || *acc.DailyLimit != 3 || *acc.MinIntervalSec != 600 || acc.CommunityCooldownH != nil {
		t.Fatalf("update %+v %v", acc, err)
	}
	zero := 0
	if acc, _ = st.UpdateAccount(u.ID, acc.ID, AccountUpdate{DailyLimit: &zero}); acc.DailyLimit != nil || *acc.MinIntervalSec != 600 {
		t.Fatalf("0 must reset to the default and leave the rest: %+v", acc)
	}
	if missing, err := st.UpdateAccount(u.ID, 999, AccountUpdate{Paused: &yes}); missing != nil || err != nil {
		t.Fatalf("missing account %+v %v", missing, err)
	}
}

func TestSetAccountStatus(t *testing.T) {
	st := openTest(t)
	u := newUser(t, st, emailA)
	acc := saveAccount(t, st, u.ID, platReddit, "me")
	until := time.Now().Add(time.Hour)
	changed, err := st.SetAccountStatus(acc.ID, AccountLimited, "429", until)
	if err != nil || !changed {
		t.Fatalf("first status %v %v", changed, err)
	}
	if changed, _ = st.SetAccountStatus(acc.ID, AccountLimited, "429 again", until); changed {
		t.Fatal("same status is not a change")
	}
	acc, _ = st.Account(u.ID, acc.ID)
	if acc.Status != AccountLimited || acc.LimitedUntil == nil || acc.CheckedAt == nil || *acc.StatusDetail != "429 again" {
		t.Fatalf("status %+v", acc)
	}
	if changed, _ = st.SetAccountStatus(acc.ID, AccountLive, "", time.Time{}); !changed {
		t.Fatal("limited → live is a change")
	}
	if acc, _ = st.Account(u.ID, acc.ID); acc.LimitedUntil != nil || acc.StatusDetail != nil {
		t.Fatalf("live must clear limited_until and detail: %+v", acc)
	}
	var events int
	var platform string
	_ = st.db.QueryRow(`SELECT COUNT(*), MAX(platform) FROM account_status_events WHERE account_id = ?`, acc.ID).Scan(&events, &platform)
	if events != 2 || platform != platReddit {
		t.Fatalf("events %d %q", events, platform)
	}
	if _, err := st.SetAccountStatus(acc.ID, "zombie", "", time.Time{}); err == nil {
		t.Fatal("unknown status accepted")
	}
	if _, err := st.SetAccountStatus(999, AccountLive, "", time.Time{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: %v", err)
	}
}

// usageFixture has account a with two publications and one claimed draft,
// and account b with only a failed one, both of one user; before precedes
// all of them.
func usageFixture(t *testing.T) (u Usage, a, b *Account, before time.Time) {
	t.Helper()
	st := openTest(t)
	owner := newUser(t, st, emailA)
	a = saveAccount(t, st, owner.ID, platReddit, "a")
	b = saveAccount(t, st, owner.ID, platReddit, "b")
	before = time.Now().Add(-time.Second)
	publishDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Community: subRGolang, Title: "t", Body: "x"}, a.ID, "https://reddit.com/r/golang/1")
	publishDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Kind: kindReply, ReplyTo: "https://reddit.com/r/go/comments/x/", Body: "y"}, a.ID, "https://reddit.com/c/2")
	claimed := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Community: subRust, Title: "t2"})
	if _, err := st.ClaimForPublish(owner.ID, claimed.ID, a.ID, nil); err != nil { // publishing counts too
		t.Fatal(err)
	}
	failed := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Community: subGolang, Title: "t3"})
	_, _ = st.ClaimForPublish(owner.ID, failed.ID, b.ID, nil)
	_, _ = st.FinishPublish(failed.ID, "", "", errors.New("HTTP 500")) // failures do not count
	return st.Usage(), a, b, before
}

func TestUsage(t *testing.T) {
	u, a, b, before := usageFixture(t)
	if n, err := u.PublishedSince(a.ID, before); err != nil || n != 3 {
		t.Fatalf("published since = %d %v", n, err)
	}
	if n, _ := u.PublishedSince(a.ID, time.Now().Add(time.Minute)); n != 0 {
		t.Fatalf("future window = %d", n)
	}
	if n, _ := u.PublishedSince(b.ID, before); n != 0 {
		t.Fatalf("failed draft counted: %d", n)
	}
	if times, _ := u.PublishedTimes(a.ID, before); len(times) != 3 || times[0].After(times[2]) {
		t.Fatalf("times %v", times)
	}
	if last, _ := u.LastPublished(a.ID); last.Before(before) {
		t.Fatalf("last %v", last)
	}
	if last, _ := u.LastPublished(b.ID); !last.IsZero() {
		t.Fatalf("b never published: %v", last)
	}
	if last, _ := u.LastPublishedIn(a.ID, "GoLang"); last.IsZero() {
		t.Fatal("r/golang and GoLang are one community")
	}
	if last, _ := u.LastPublishedIn(a.ID, "python"); !last.IsZero() {
		t.Fatal("never published in python")
	}
}

type boolCase struct {
	name string
	got  func() (bool, error)
	want bool
}

func checkBools(t *testing.T, cases []boolCase) {
	t.Helper()
	for _, c := range cases {
		if got, err := c.got(); err != nil || got != c.want {
			t.Errorf("%s: got %v (%v), want %v", c.name, got, err, c.want)
		}
	}
}

func TestUsageOtherAccounts(t *testing.T) {
	u, a, b, before := usageFixture(t)
	thread := "https://reddit.com/r/go/comments/x"
	checkBools(t, []boolCase{
		{"a published in r/golang", func() (bool, error) { return u.OtherAccountInCommunity(platReddit, b.ID, subRGolang, before) }, true},
		{"an account is not another account", func() (bool, error) { return u.OtherAccountInCommunity(platReddit, a.ID, subGolang, before) }, false},
		{"outside the window", func() (bool, error) {
			return u.OtherAccountInCommunity(platReddit, b.ID, subGolang, time.Now().Add(time.Minute))
		}, false},
		{"a replied in this thread", func() (bool, error) { return u.OtherAccountInThread(platReddit, b.ID, thread) }, true},
		{"replying to a post of our own other account is the same thread", func() (bool, error) {
			return u.OtherAccountInThread(platReddit, b.ID, "https://reddit.com/r/golang/1")
		}, true},
		{"the same account may reply again", func() (bool, error) { return u.OtherAccountInThread(platReddit, a.ID, thread) }, false},
		{"other platform", func() (bool, error) { return u.OtherAccountInThread(platBluesky, b.ID, thread) }, false},
	})
	if last, err := u.LastPublishedInByOthers(platReddit, b.ID, subGolang); err != nil || last.Before(before) {
		t.Fatalf("a's publication in golang, seen from b: %v %v", last, err)
	}
}

// Another user's accounts are never "another account": the rules keep one
// person's accounts apart, not everyone's.
func TestUsageIgnoresOtherUsers(t *testing.T) {
	st := openTest(t)
	alice, bob := newUser(t, st, emailA), newUser(t, st, emailB)
	a := saveAccount(t, st, alice.ID, platReddit, "a")
	b := saveAccount(t, st, bob.ID, platReddit, "b")
	post := publishDraft(t, st, NewDraft{UserID: alice.ID, Platform: platReddit, Community: subGolang, Title: titleLaunch, Body: "x"}, a.ID, "https://r/1")
	reply := publishDraft(t, st, NewDraft{UserID: alice.ID, Platform: platReddit, Kind: kindReply, ReplyTo: threadURL, Body: "y"}, a.ID, "https://r/2")
	_, _ = st.MarkRemoved(post.ID)
	_, _ = st.MarkRemoved(reply.ID)
	u := st.Usage()
	checkBools(t, []boolCase{
		{"community", func() (bool, error) { return u.OtherAccountInCommunity(platReddit, b.ID, subGolang, time.Time{}) }, false},
		{"thread", func() (bool, error) { return u.OtherAccountInThread(platReddit, b.ID, threadURL) }, false},
		{"own post as thread", func() (bool, error) { return u.OtherAccountInThread(platReddit, b.ID, "https://r/1") }, false},
	})
	if last, err := u.LastPublishedInByOthers(platReddit, b.ID, subGolang); err != nil || !last.IsZero() {
		t.Fatalf("another user's publication counted: %v %v", last, err)
	}
	for _, target := range [][3]string{{threadURL, "", ""}, {"", subGolang, "launch"}} {
		if h, err := u.RemovedFromOtherAccount(platReddit, b.ID, target[0], target[1], target[2]); h != "" || err != nil {
			t.Fatalf("another user's removal counted for %v: %q %v", target, h, err)
		}
	}
	// The same user's second account does see it.
	a2 := saveAccount(t, st, alice.ID, platReddit, "a2")
	if h, _ := u.RemovedFromOtherAccount(platReddit, a2.ID, threadURL, "", ""); h != "a" {
		t.Fatalf("own removal from a: %q", h)
	}
}

func TestClaimForPublish(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	acc := saveAccount(t, st, owner.ID, platBluesky, "me")
	other := saveAccount(t, st, owner.ID, platMastodon, "me")
	d := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platBluesky})

	refused := errors.New("limit")
	if _, err := st.ClaimForPublish(owner.ID, d.ID, acc.ID, func(Usage) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("guard error: %v", err)
	}
	if d, _ = st.Draft(owner.ID, d.ID); d.Status != DraftApproved || d.AccountID != nil {
		t.Fatalf("a refused claim must change nothing: %+v", d)
	}
	if _, err := st.ClaimForPublish(owner.ID, d.ID, other.ID, nil); err == nil {
		t.Fatal("account of another platform accepted")
	}
	if _, err := st.ClaimForPublish(owner.ID, d.ID, 999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: %v", err)
	}
	if _, err := st.ClaimForPublish(owner.ID, 999, acc.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing draft: %v", err)
	}
	claimed, err := st.ClaimForPublish(owner.ID, d.ID, acc.ID, func(u Usage) error {
		_, err := u.PublishedSince(acc.ID, time.Now().Add(-time.Hour))
		return err
	})
	if err != nil || claimed.Status != DraftPublishing || *claimed.AccountID != acc.ID {
		t.Fatalf("claim %+v %v", claimed, err)
	}
	if _, err := st.ClaimForPublish(owner.ID, d.ID, acc.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
}

func TestClaimForPublishIsPerUser(t *testing.T) {
	st := openTest(t)
	alice, bob := newUser(t, st, emailA), newUser(t, st, emailB)
	aliceAcc := saveAccount(t, st, alice.ID, platBluesky, "alice")
	bobAcc := saveAccount(t, st, bob.ID, platBluesky, "bob")
	aliceDraft := approvedDraft(t, st, NewDraft{UserID: alice.ID, Platform: platBluesky})
	bobDraft := approvedDraft(t, st, NewDraft{UserID: bob.ID, Platform: platBluesky})
	for name, c := range map[string]struct{ user, draft, account int64 }{
		"another user's draft":   {bob.ID, aliceDraft.ID, bobAcc.ID},
		"another user's account": {bob.ID, bobDraft.ID, aliceAcc.ID},
	} {
		if _, err := st.ClaimForPublish(c.user, c.draft, c.account, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, d := range []*Draft{aliceDraft, bobDraft} {
		if got, _ := st.Draft(0, d.ID); got.Status != DraftApproved || got.AccountID != nil {
			t.Fatalf("a refused claim changed draft %d: %+v", d.ID, got)
		}
	}
	// The server itself (user 0) may claim anyone's draft with its owner's account.
	if got, err := st.ClaimForPublish(0, aliceDraft.ID, aliceAcc.ID, nil); err != nil || got.Status != DraftPublishing {
		t.Fatalf("claim as the server: %+v %v", got, err)
	}
}

func TestReturnToApprovedAndMarkRemoved(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	acc := saveAccount(t, st, owner.ID, platBluesky, "me")
	d := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platBluesky})
	if _, err := st.ClaimForPublish(owner.ID, d.ID, acc.ID, nil); err != nil {
		t.Fatal(err)
	}

	back, err := st.ReturnToApproved(d.ID, "rate limited")
	if err != nil || back.Status != DraftApproved || *back.Error != "rate limited" || *back.AccountID != acc.ID {
		t.Fatalf("return %+v %v", back, err)
	}
	if _, err := st.ReturnToApproved(d.ID, "again"); !errors.Is(err, ErrConflict) {
		t.Fatal("only a claimed draft goes back")
	}
	if ok, err := st.MarkRemoved(d.ID); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("an unpublished draft cannot be removed: %v", err)
	}
	_, _ = st.ClaimForPublish(owner.ID, d.ID, acc.ID, nil)
	_, _ = st.FinishPublish(d.ID, "r", someURL, nil)
	if ok, err := st.MarkRemoved(d.ID); !ok || err != nil {
		t.Fatalf("mark removed %v %v", ok, err)
	}
	if ok, err := st.MarkRemoved(d.ID); ok || err != nil {
		t.Fatalf("second mark %v %v", ok, err)
	}
	if d, _ = st.Draft(owner.ID, d.ID); d.RemovedAt == nil || d.Status != DraftPublished {
		t.Fatalf("removed draft %+v", d)
	}
	if _, err := st.MarkRemoved(999); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing draft")
	}
}

func TestRemovedFromOtherAccount(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	a := saveAccount(t, st, owner.ID, platReddit, "a")
	b := saveAccount(t, st, owner.ID, platReddit, "b")
	post := publishDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Community: subRGolang, Title: titleLaunch, Body: "x"}, a.ID, "https://r/1")
	reply := publishDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Kind: kindReply, ReplyTo: threadURL, Body: "y"}, a.ID, "https://r/2")
	u := st.Usage()
	if h, _ := u.RemovedFromOtherAccount(platReddit, b.ID, threadBare, "", ""); h != "" {
		t.Fatal("nothing removed yet")
	}
	_, _ = st.MarkRemoved(post.ID)
	_, _ = st.MarkRemoved(reply.ID)
	for _, c := range []struct {
		account                   int64
		replyTo, community, title string
		want                      string
	}{
		{b.ID, threadBare, "", "", "a"},            // reply removed from a
		{b.ID, "", subGolang, " launch ", "a"},     // post removed from a
		{b.ID, "", subRust, titleLaunch, ""},       // other community
		{a.ID, threadBare, "", "", ""},             // the same account is not another account
		{b.ID, "https://r/other", "", "", ""},      // other thread
		{b.ID, "", subGolang, "Something", ""},     // other title
		{b.ID, "", "", "", ""},                     // no target
		{b.ID, " ", " ", " ", ""},                  // blank target
		{b.ID, "https://r/t/", subGolang, "", "a"}, // the thread wins over the community
	} {
		if h, err := u.RemovedFromOtherAccount(platReddit, c.account, c.replyTo, c.community, c.title); h != c.want || err != nil {
			t.Errorf("%+v: %q %v", c, h, err)
		}
	}
}

// Two radaro processes publishing at once against a daily limit of one:
// the IMMEDIATE transaction lets exactly one through.
func TestConcurrentClaimsRespectTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st1.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	owner := newUser(t, st1, emailA)
	acc := saveAccount(t, st1, owner.ID, platBluesky, "me")
	d1 := approvedDraft(t, st1, NewDraft{UserID: owner.ID, Platform: platBluesky, Body: "one"})
	d2 := approvedDraft(t, st1, NewDraft{UserID: owner.ID, Platform: platBluesky, Body: "two"})

	errLimit := errors.New("daily limit reached")
	guard := func(u Usage) error {
		n, err := u.PublishedSince(acc.ID, time.Now().Add(-24*time.Hour))
		if err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond) // widen the race window
		if n >= 1 {
			return errLimit
		}
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, c := range []struct {
		st *Store
		id int64
	}{{st1, d1.ID}, {st2, d2.ID}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.st.ClaimForPublish(owner.ID, c.id, acc.ID, guard)
		}()
	}
	wg.Wait()
	if won := countWins(t, errs, errLimit); won != 1 {
		t.Fatalf("%d claims passed a daily limit of 1: %v", won, errs)
	}
}

// countWins counts nil errors; any error other than refused fails the test.
func countWins(t *testing.T, errs []error, refused error) int {
	t.Helper()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, refused):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return won
}

func TestUpdateAccountDetailAndValidation(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	acc := saveAccount(t, st, owner.ID, platDevto, "me")
	minus := -1
	if _, err := st.UpdateAccount(owner.ID, acc.ID, AccountUpdate{MinIntervalSec: &minus}); err == nil {
		t.Fatal("negative limit accepted")
	}
	note := "on holiday"
	if acc, _ = st.UpdateAccount(owner.ID, acc.ID, AccountUpdate{StatusDetail: &note}); acc.StatusDetail == nil || *acc.StatusDetail != note {
		t.Fatalf("detail %+v", acc)
	}
	empty := ""
	if acc, _ = st.UpdateAccount(owner.ID, acc.ID, AccountUpdate{StatusDetail: &empty}); acc.StatusDetail != nil {
		t.Fatalf("an empty detail clears it: %+v", acc)
	}
}

func TestUpdateAccountIsPerUser(t *testing.T) {
	st := openTest(t)
	alice, bob := newUser(t, st, emailA), newUser(t, st, emailB)
	acc := saveAccount(t, st, alice.ID, platDevto, "alice")
	yes, one := true, 1
	if got, err := st.UpdateAccount(bob.ID, acc.ID, AccountUpdate{Paused: &yes, DailyLimit: &one}); got != nil || err != nil {
		t.Fatalf("B updated A's account: %+v %v", got, err)
	}
	if got, _ := st.Account(alice.ID, acc.ID); got.Paused || got.DailyLimit != nil {
		t.Fatalf("A's account changed: %+v", got)
	}
	if got, err := st.UpdateAccount(0, acc.ID, AccountUpdate{Paused: &yes}); err != nil || got == nil || !got.Paused {
		t.Fatalf("the server (user 0) may update any account: %+v %v", got, err)
	}
}

// Loaded accounts and drafts know their owner; unowned ones have 0.
func TestOwnerIsLoaded(t *testing.T) {
	st := openTest(t)
	unowned := saveAccount(t, st, 0, platDevto, "legacy")
	legacy := approvedDraft(t, st, NewDraft{Platform: platDevto, Title: "t"})
	owner := newUser(t, st, emailA) // takes over the unowned rows
	acc := saveAccount(t, st, owner.ID, platDevto, "mine")
	d := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platDevto, Title: "t"})
	if got, _ := st.Account(0, acc.ID); got.UserID != owner.ID {
		t.Fatalf("account owner %d, want %d", got.UserID, owner.ID)
	}
	if got, _ := st.Draft(0, d.ID); got.UserID != owner.ID {
		t.Fatalf("draft owner %d, want %d", got.UserID, owner.ID)
	}
	if got, _ := st.Account(0, unowned.ID); got.UserID != owner.ID {
		t.Fatalf("taken-over account owner %d", got.UserID)
	}
	if got, _ := st.Draft(0, legacy.ID); got.UserID != owner.ID {
		t.Fatalf("taken-over draft owner %d", got.UserID)
	}
	bare := saveAccount(t, st, 0, platDevto, "server")
	if got, _ := st.Account(0, bare.ID); got.UserID != 0 {
		t.Fatalf("an unowned account has owner %d", got.UserID)
	}
	if pool, _ := st.Accounts(owner.ID, platDevto); len(pool) != 2 || pool[0].UserID != owner.ID {
		t.Fatalf("listed accounts %+v", pool)
	}
}

func TestUsageWithoutTarget(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	a := saveAccount(t, st, owner.ID, platReddit, "a")
	b := saveAccount(t, st, owner.ID, platReddit, "b")
	d := publishDraft(t, st, NewDraft{UserID: owner.ID, Platform: platReddit, Community: subGolang, Title: "t", Body: "x"}, a.ID, "https://r/1")
	_, _ = st.MarkRemoved(d.ID)
	u := st.Usage()
	if in, err := u.OtherAccountInThread(platReddit, b.ID, "  "); in || err != nil {
		t.Fatalf("no thread: %v %v", in, err)
	}
	if h, err := u.RemovedFromOtherAccount(platReddit, b.ID, "", subGolang, " "); h != "" || err != nil {
		t.Fatalf("neither a thread nor a title matches nothing: %q %v", h, err)
	}
}

func TestNormCommunity(t *testing.T) {
	for in, want := range map[string]string{" r/GoLang ": subGolang, "Rust": subRust, "": "", "r/": ""} {
		if got := NormCommunity(in); got != want {
			t.Errorf("NormCommunity(%q) = %q, want %q", in, got, want)
		}
	}
}

// A database failure is an error from every query, never an empty answer
// that would let a publication through.
func TestClosedStoreErrors(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	acc := saveAccount(t, st, owner.ID, platDevto, "me")
	d := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platDevto, Title: "t"})
	pinned := approvedDraft(t, st, NewDraft{UserID: owner.ID, Platform: platDevto, Title: "p", AccountID: acc.ID})
	st.Close()
	yes, now := true, time.Now()
	u := st.Usage()
	for name, err := range map[string]error{
		"UpdateAccount":        second(st.UpdateAccount(owner.ID, acc.ID, AccountUpdate{Paused: &yes})),
		"SetAccountStatus":     second(st.SetAccountStatus(acc.ID, AccountLive, "", time.Time{})),
		"PublishedSince":       second(u.PublishedSince(acc.ID, now)),
		"PublishedTimes":       second(u.PublishedTimes(acc.ID, now)),
		"LastPublished":        second(u.LastPublished(acc.ID)),
		"OtherInThread":        second(u.OtherAccountInThread(platDevto, acc.ID, someURL)),
		"OtherInCommunity":     second(u.OtherAccountInCommunity(platDevto, acc.ID, "c", now)),
		"RemovedFromOther":     second(u.RemovedFromOtherAccount(platDevto, acc.ID, someURL, "", "")),
		"ClaimForPublish":      second(st.ClaimForPublish(owner.ID, d.ID, acc.ID, nil)),
		"ReturnToApproved":     second(st.ReturnToApproved(d.ID, "")),
		"MarkRemoved":          second(st.MarkRemoved(d.ID)),
		"SetDraftAccount":      second(st.SetDraftAccount(owner.ID, d.ID, nil)),
		"SetDraftAccountPin":   second(st.SetDraftAccount(owner.ID, d.ID, &acc.ID)),
		"PublishedDrafts":      second(st.PublishedDrafts(owner.ID, platDevto, now)),
		"AccountActivity":      second(st.AccountActivity(owner.ID, now)),
		"BanRateSeries":        second(st.BanRateSeries(owner.ID, 7, now)),
		"ProjectPool":          second(st.ProjectPool(owner.ID, DefaultProjectID, platDevto)),
		"UnbindProjectAccount": second(st.UnbindProjectAccount(owner.ID, DefaultProjectID, acc.ID)),
		"BindAccount":          second(st.BindAccount(owner.ID, DefaultProjectID, acc.ID)),
		"UnbindAccount":        second(st.UnbindAccount(owner.ID, DefaultProjectID, platDevto)),
		"PublishAccount":       second(st.PublishAccount(owner.ID, d)),
		"PublishAccountPin":    second(st.PublishAccount(owner.ID, pinned)),
	} {
		if err == nil {
			t.Errorf("%s on a closed store: no error", name)
		}
	}
}

func second[T any](_ T, err error) error { return err }
