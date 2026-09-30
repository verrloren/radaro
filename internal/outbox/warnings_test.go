package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

type fakeRules struct {
	rules []publish.SubredditRule
	err   error
	calls *int
	subs  *[]string
}

func (f fakeRules) Publish(context.Context, publish.Post) (publish.Result, error) {
	return publish.Result{}, errors.New("not used")
}
func (f fakeRules) Metrics(context.Context, string) (publish.Metrics, error) { return nil, nil }
func (f fakeRules) SubredditRules(_ context.Context, sub string) ([]publish.SubredditRule, error) {
	*f.calls++
	*f.subs = append(*f.subs, sub)
	return f.rules, f.err
}

// warnService is a Service over an empty store with one user, whose id it returns.
func warnService(t *testing.T) (*Service, *store.Store, int64) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rulesCache.Lock()
	rulesCache.m = map[string]cachedRules{}
	rulesCache.Unlock()
	u, err := st.CreateUser("owner@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, "test"), st, u.ID
}

func account(t *testing.T, st *store.Store, uid int64, platform, handle string) *store.Account {
	t.Helper()
	acc, err := st.SaveAccount(uid, platform, handle, map[string]string{"token": "secret-" + handle})
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func published(t *testing.T, st *store.Store, n store.NewDraft) *store.Draft {
	t.Helper()
	d, err := st.CreateDraft(n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveDraft(n.UserID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginPublish(n.UserID, d.ID); err != nil {
		t.Fatal(err)
	}
	d, err = st.FinishPublish(d.ID, "id", "https://example.test/p/"+strings.Repeat("x", int(d.ID)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func codes(ws []Warning) map[string]int {
	out := map[string]int{}
	for _, w := range ws {
		out[w.Code]++
	}
	return out
}

func TestSelfPromoShare(t *testing.T) {
	s, st, uid := warnService(t)
	me := account(t, st, uid, "bluesky", "me.bsky.social")
	for i := 0; i < 3; i++ {
		published(t, st, store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: me.ID, Kind: "post", Body: "Unrelated thoughts on SQLite number " + strings.Repeat("!", i)})
	}
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: me.ID, Kind: "post", Query: "radaro",
		Body: "I built Radaro for this: https://github.com/verrloren/radaro"})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil {
		t.Fatal(err)
	}
	if codes(ws)[WarnSelfPromo] != 0 {
		t.Fatalf("1 of 4 is within the limit: %+v", ws)
	}

	// A link to a page under the project's link counts as the project.
	published(t, st, store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: me.ID, Kind: "post",
		Body: "See the docs at https://www.github.com/verrloren/radaro/blob/main/README.md."})
	ws, _ = s.Warnings(context.Background(), uid, d)
	if codes(ws)[WarnSelfPromo] != 1 {
		t.Fatalf("2 of 5 is above the limit: %+v", ws)
	}
	if !strings.Contains(ws[0].Text, "2 of 5") || !strings.Contains(ws[0].Text, "me.bsky.social") {
		t.Fatalf("text %q", ws[0].Text)
	}

	// The keyword must be a whole word to count.
	other := account(t, st, uid, "bluesky", "other.bsky.social")
	for i := 0; i < 3; i++ {
		published(t, st, store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: other.ID, Kind: "post", Body: "radarology is not it " + strings.Repeat("?", i)})
	}
	d2, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: other.ID, Kind: "post", Query: "radaro", Body: "Radaro is neat"})
	ws, _ = s.Warnings(context.Background(), uid, d2)
	if codes(ws)[WarnSelfPromo] != 0 {
		t.Fatalf("substring matched as the keyword: %+v", ws)
	}

	// A draft that does not promote anything is not flagged.
	d3, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: me.ID, Kind: "post", Body: "Happy Friday"})
	if ws, _ := s.Warnings(context.Background(), uid, d3); codes(ws)[WarnSelfPromo] != 0 {
		t.Fatalf("plain draft flagged: %+v", ws)
	}
}

func TestDuplicatesFromOtherAccounts(t *testing.T) {
	s, st, uid := warnService(t)
	me := account(t, st, uid, "mastodon", "me@social")
	other := account(t, st, uid, "mastodon", "other@social")
	text := "Radaro watches Hacker News, Reddit and Bluesky for mentions of your project and drafts replies"
	published(t, st, store.NewDraft{UserID: uid, Platform: "mastodon", AccountID: other.ID, Kind: "post", Body: text + " for you."})
	published(t, st, store.NewDraft{UserID: uid, Platform: "mastodon", AccountID: me.ID, Kind: "post", Body: text})
	published(t, st, store.NewDraft{UserID: uid, Platform: "mastodon", AccountID: other.ID, Kind: "post", Body: "Totally different words, see https://example.com/tool?ref=1"})
	published(t, st, store.NewDraft{UserID: uid, Platform: "bluesky", AccountID: account(t, st, uid, "bluesky", "b").ID, Kind: "post", Body: text})

	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "mastodon", AccountID: me.ID, Kind: "post", Body: text + ". Try it: http://example.com/tool/"})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil {
		t.Fatal(err)
	}
	var dup []string
	for _, w := range ws {
		if w.Code == WarnDuplicate {
			dup = append(dup, w.Text)
		}
	}
	if len(dup) != 2 {
		t.Fatalf("want the near-duplicate text and the shared link from other@social only: %q", dup)
	}
	joined := strings.Join(dup, "\n")
	if !strings.Contains(joined, "Nearly the same text") || !strings.Contains(joined, "example.com/tool") || strings.Contains(joined, "me@social") {
		t.Fatalf("warnings %q", dup)
	}
	for _, w := range ws {
		if strings.Contains(w.Text, "secret-") {
			t.Fatal("credentials leaked into a warning")
		}
	}
}

func TestSubredditRules(t *testing.T) {
	s, st, uid := warnService(t)
	acc := account(t, st, uid, "reddit", "me")
	calls := 0
	var subs []string
	var gotCreds json.RawMessage
	fake := fakeRules{rules: []publish.SubredditRule{
		{Name: "Be civil", Description: "No personal attacks."},
		{Name: "No self-promotion", Description: "Posts about your own project are removed."},
		{Name: "Weekly thread", Description: "Advertising goes in the weekly thread."},
	}, calls: &calls, subs: &subs}
	s.NewPublisher = func(platform string, creds json.RawMessage, _ string) (publish.Publisher, error) {
		gotCreds = creds
		return fake, nil
	}
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", AccountID: acc.ID, Kind: "post", Community: "r/golang", Title: "t", Body: "b"})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil {
		t.Fatal(err)
	}
	c := codes(ws)
	if c[WarnSubredditRules] != 1 || c[WarnCommunityBansPromo] != 2 {
		t.Fatalf("rules %+v", ws)
	}
	if subs[0] != "golang" || !strings.Contains(string(gotCreds), "secret-me") {
		t.Fatalf("asked for %q with %s", subs, gotCreds)
	}

	// Cached: a reply in the same subreddit (from its thread URL) does not ask again.
	reply, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", AccountID: acc.ID, Kind: "reply", Body: "b",
		ReplyTo: "https://www.reddit.com/r/GoLang/comments/abc/title/"})
	if _, err := s.Warnings(context.Background(), uid, reply); err != nil || calls != 1 {
		t.Fatalf("cache: %d calls, %v", calls, err)
	}
	// Expired: asks again.
	s.Now = func() time.Time { return time.Now().Add(2 * rulesTTL) }
	_, _ = s.Warnings(context.Background(), uid, reply)
	if calls != 2 {
		t.Fatalf("expired cache: %d calls", calls)
	}

	// A failure is advice too, not an error, and is not cached.
	s.Now = time.Now
	failing := fakeRules{err: errors.New("HTTP 500"), calls: &calls, subs: &subs}
	s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) { return failing, nil }
	d2, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", AccountID: acc.ID, Kind: "post", Community: "rust", Title: "t", Body: "b"})
	for i := 0; i < 2; i++ {
		ws, err = s.Warnings(context.Background(), uid, d2)
		if err != nil || codes(ws)[WarnRulesUnavailable] != 1 {
			t.Fatalf("unavailable %+v %v", ws, err)
		}
	}
	if calls != 4 {
		t.Fatalf("errors must not be cached: %d calls", calls)
	}

	// Other platforms have no subreddit rules.
	bs, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "bluesky", Kind: "post", Body: "b"})
	if ws, _ := s.Warnings(context.Background(), uid, bs); len(ws) != 0 {
		t.Fatalf("bluesky %+v", ws)
	}
}

func TestPageLinksAndShingles(t *testing.T) {
	got := pageLinks("See https://WWW.Example.com/a/b/?x=1#y, and (http://example.com/a/b). Also https://x.io")
	if len(got) != 2 || got[0] != "example.com/a/b" || got[1] != "x.io" {
		t.Fatalf("links %q", got)
	}
	a := shingles("one two three four")
	if jaccard(a, shingles("One, two three four!")) != 1 || jaccard(a, shingles("five six seven")) != 0 {
		t.Fatal("jaccard")
	}
	if !wordPattern("c++").MatchString("I like C++.") || wordPattern("go").MatchString("gopher") {
		t.Fatal("word pattern")
	}
}

func TestSubredditRulesNeedAnAccount(t *testing.T) {
	s, st, uid := warnService(t)
	s.NewPublisher = func(string, json.RawMessage, string) (publish.Publisher, error) {
		t.Fatal("no connector without an account")
		return nil, nil
	}
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", Kind: "post", Community: "golang", Title: "t", Body: "b"})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil || len(ws) != 1 || ws[0].Code != WarnRulesUnavailable {
		t.Fatalf("%+v %v", ws, err)
	}
}

// Without an account to publish from (all paused), the rules are read with
// any Reddit account that still works, never a dead one.
func TestSubredditRulesUseAnyWorkingAccount(t *testing.T) {
	s, st, uid := warnService(t)
	dead := account(t, st, uid, "reddit", "dead")
	_, _ = st.SetAccountStatus(dead.ID, store.AccountInvalid, "401", time.Time{})
	paused := account(t, st, uid, "reddit", "paused")
	yes := true
	_, _ = st.UpdateAccount(uid, paused.ID, store.AccountUpdate{Paused: &yes})
	var gotCreds json.RawMessage
	calls := 0
	var subs []string
	s.NewPublisher = func(_ string, creds json.RawMessage, _ string) (publish.Publisher, error) {
		gotCreds = creds
		return fakeRules{rules: []publish.SubredditRule{{Name: "Be civil"}}, calls: &calls, subs: &subs}, nil
	}
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", Kind: "post", Community: "golang", Title: "t", Body: "b"})
	ws, err := s.Warnings(context.Background(), uid, d)
	if err != nil || codes(ws)[WarnSubredditRules] != 1 || !strings.Contains(string(gotCreds), "secret-paused") {
		t.Fatalf("%+v %v with %s", ws, err, gotCreds)
	}
}

func TestSubredditRulesConnectorProblems(t *testing.T) {
	s, st, uid := warnService(t)
	acc := account(t, st, uid, "reddit", "me")
	for sub, newPub := range map[string]func(string, json.RawMessage, string) (publish.Publisher, error){
		"noconnector": func(string, json.RawMessage, string) (publish.Publisher, error) {
			return nil, errors.New("bad credentials")
		},
		"norules": func(string, json.RawMessage, string) (publish.Publisher, error) { return &fakePub{}, nil },
	} {
		s.NewPublisher = newPub
		d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "reddit", AccountID: acc.ID, Kind: "post", Community: sub, Title: "t", Body: "b"})
		ws, err := s.Warnings(context.Background(), uid, d)
		if err != nil || len(ws) != 1 || ws[0].Code != WarnRulesUnavailable {
			t.Fatalf("%s: %+v %v", sub, ws, err)
		}
	}
}

func TestWarningsStoreFailure(t *testing.T) {
	s, st, uid := warnService(t)
	d, _ := st.CreateDraft(store.NewDraft{UserID: uid, Platform: "bluesky", Kind: "post", Body: "b"})
	st.Close()
	if _, err := s.Warnings(context.Background(), uid, d); err == nil {
		t.Fatal("a store failure is an error")
	}
}
