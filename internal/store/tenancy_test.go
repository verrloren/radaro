package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

func TestFirstUserTakesOverExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Data from before users existed (or from admin commands): unowned.
	if err := st.SaveTracking(0, "golang", []string{"hackernews"}, 0); err != nil {
		t.Fatal(err)
	}
	acme, _ := st.CreateProject(0, "Acme")
	acc, _ := st.SaveAccount(0, "devto", "me", map[string]string{"k": "v"})
	d, _ := st.CreateDraft(NewDraft{Platform: "devto", AccountID: acc.ID, Kind: "post", Body: "b"})
	_ = st.LogActivity(0, "draft.created", d.ID, "x")

	owner, err := st.CreateUser("owner@example.com", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := st.Projects(owner.ID)
	if len(ps) != 2 || !ps[0].IsDefault || ps[0].ID != DefaultProjectID || ps[1].ID != acme.ID {
		t.Fatalf("owner's projects = %+v", ps)
	}
	if qs, _ := st.Queries(owner.ID, 0); len(qs) != 1 || qs[0] != "golang" {
		t.Fatalf("owner's keywords = %v", qs)
	}
	if a, _ := st.Account(owner.ID, acc.ID); a == nil {
		t.Fatal("owner did not get the account")
	}
	if got, _ := st.Draft(owner.ID, d.ID); got == nil {
		t.Fatal("owner did not get the draft")
	}
	if acts, _ := st.Activities(owner.ID, 10); len(acts) != 1 {
		t.Fatal("owner did not get the activity log")
	}
}

func TestUsersAreIsolated(t *testing.T) {
	st := openTest(t)
	a, _ := st.CreateUser("a@example.com", "h", true)
	b, _ := st.CreateUser("b@example.com", "h", true)

	defA, err := st.DefaultProjectFor(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defB, err := st.DefaultProjectFor(b.ID)
	if err != nil || defA == defB {
		t.Fatalf("defaults %d %d, %v", defA, defB, err)
	}
	// Both may use the same project name.
	pa, err := st.CreateProject(a.ID, "Launch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(b.ID, "launch"); err != nil {
		t.Fatalf("B could not reuse A's project name: %v", err)
	}
	if _, err := st.CreateProject(a.ID, "LAUNCH"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate within one user: %v", err)
	}

	if err := st.SaveTracking(a.ID, "secret-product", []string{"hackernews"}, pa.ID); err != nil {
		t.Fatal(err)
	}
	m := &model.Mention{Source: "hackernews", Query: "secret-product", Text: "t", URL: model.Str("https://x"), CreatedAt: time.Now()}
	m.Normalize()
	if _, err := st.Upsert([]*model.Mention{m}, true); err != nil {
		t.Fatal(err)
	}
	// A project of A is invisible and untouchable for B.
	if p, _ := st.Project(b.ID, pa.ID); p != nil {
		t.Fatal("B read A's project")
	}
	if ok, _ := st.DeleteProject(b.ID, pa.ID); ok {
		t.Fatal("B deleted A's project")
	}
	if _, err := st.RenameProject(b.ID, pa.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B renamed A's project: %v", err)
	}
	if err := st.SaveTracking(b.ID, "other", []string{"hackernews"}, pa.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B filed a keyword into A's project: %v", err)
	}
	if _, err := st.AddQueryToProject(b.ID, pa.ID, "secret-product", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B added to A's project: %v", err)
	}
	// Mentions and keywords follow project ownership.
	if qs, _ := st.Queries(b.ID, 0); len(qs) != 0 {
		t.Fatalf("B sees keywords %v", qs)
	}
	if ok, _ := st.OwnsQuery(b.ID, "secret-product"); ok {
		t.Fatal("B owns A's keyword")
	}
	for _, sc := range []Scope{{UserID: b.ID}, {UserID: b.ID, Query: "secret-product"}, {UserID: b.ID, ProjectID: pa.ID}} {
		if ms, _ := st.Mentions(MentionFilter{Scope: sc}); len(ms) != 0 {
			t.Fatalf("B read mentions with %+v", sc)
		}
		if sum, _ := st.Summary(sc); sum.Total != 0 {
			t.Fatalf("B summary with %+v = %d", sc, sum.Total)
		}
	}
	if ms, _ := st.Mentions(MentionFilter{Scope: Scope{UserID: a.ID}}); len(ms) != 1 {
		t.Fatalf("A sees %d mentions", len(ms))
	}
	// Tracking the same keyword is fine: B gets it in their own Default.
	if err := st.SaveTracking(b.ID, "secret-product", []string{"hackernews"}, 0); err != nil {
		t.Fatal(err)
	}
	if ms, _ := st.Mentions(MentionFilter{Scope: Scope{UserID: b.ID, ProjectID: defB}}); len(ms) != 1 {
		t.Fatal("B does not share the scanned mentions of a keyword both track")
	}

	// Accounts and drafts.
	acc, _ := st.SaveAccount(a.ID, "devto", "alice", map[string]string{"k": "v"})
	if got, _ := st.Account(b.ID, acc.ID); got != nil {
		t.Fatal("B read A's account")
	}
	if list, _ := st.Accounts(b.ID, ""); len(list) != 0 {
		t.Fatal("B lists A's accounts")
	}
	if ok, _ := st.DeleteAccount(b.ID, acc.ID); ok {
		t.Fatal("B deleted A's account")
	}
	if _, err := st.CreateDraft(NewDraft{UserID: b.ID, Platform: "devto", AccountID: acc.ID, Kind: "post", Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B drafted with A's account: %v", err)
	}
	d, err := st.CreateDraft(NewDraft{UserID: a.ID, Platform: "devto", AccountID: acc.ID, Kind: "post", Body: "x", MentionID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Draft(b.ID, d.ID); got != nil {
		t.Fatal("B read A's draft")
	}
	if _, err := st.ApproveDraft(b.ID, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B approved A's draft: %v", err)
	}
	if list, _ := st.Drafts(b.ID, "", 0); len(list) != 0 {
		t.Fatal("B lists A's drafts")
	}
	if ids, _ := st.DraftedMentionIDs(b.ID); ids["m1"] {
		t.Fatal("A's drafted mentions leak to B")
	}
	_ = st.LogActivity(a.ID, "draft.created", d.ID, "x")
	if acts, _ := st.Activities(b.ID, 10); len(acts) != 0 {
		t.Fatal("B reads A's activity")
	}
	// The same handle can be connected by two users.
	if _, err := st.SaveAccount(b.ID, "devto", "alice", map[string]string{"k": "w"}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultProjectIsProtectedPerUser(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("a@example.com", "h", true)
	def, _ := st.DefaultProjectFor(u.ID)
	if _, err := st.DeleteProject(u.ID, def); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted the Default project: %v", err)
	}
}

func TestKeywordsCRUD(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("a@example.com", "h", true)
	other, _ := st.CreateUser("b@example.com", "h", true)
	p, _ := st.CreateProject(u.ID, "Launch")
	n, err := st.AddKeywords(u.ID, p.ID, []string{"radaro", "  social   listening ", "Radaro"}, []string{"HackerNews", "reddit"})
	if err != nil || n != 2 {
		t.Fatalf("added %d, %v", n, err)
	}
	ks, err := st.Keywords(u.ID, p.ID)
	if err != nil || len(ks) != 2 || ks[1].Query != "social listening" || len(ks[0].Sources) != 2 || ks[0].Sources[0] != "hackernews" {
		t.Fatalf("keywords = %+v, %v", ks, err)
	}
	if _, err := st.Keywords(other.ID, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user listed keywords: %v", err)
	}
	if _, err := st.AddKeywords(other.ID, p.ID, []string{"x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user added keywords: %v", err)
	}
	if ok, _ := st.RemoveKeyword(other.ID, p.ID, ks[0].ID); ok {
		t.Fatal("other user removed a keyword")
	}
	for _, bad := range []string{"", "   ", string(make([]rune, 201))} {
		if _, err := st.AddKeywords(u.ID, p.ID, []string{bad}, nil); err == nil {
			t.Fatalf("accepted keyword %q", bad)
		}
	}
	if ok, err := st.RemoveKeyword(u.ID, p.ID, ks[0].ID); err != nil || !ok {
		t.Fatalf("remove = %v, %v", ok, err)
	}
	if ks, _ := st.Keywords(u.ID, p.ID); len(ks) != 1 {
		t.Fatalf("after remove %+v", ks)
	}
	if tr, _ := st.Tracking("radaro"); tr == nil {
		t.Fatal("removing a keyword dropped its scan data")
	}
	if ok, _ := st.DeleteProject(u.ID, p.ID); !ok {
		t.Fatal("delete failed")
	}
	if ok, _ := st.OwnsQuery(u.ID, "social listening"); ok {
		t.Fatal("keywords of a deleted project are still owned")
	}
}

func TestKeywordAndProjectLimits(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser("a@example.com", "h", true)
	p, _ := st.CreateProject(u.ID, "Big")
	many := make([]string, MaxKeywordsPerProject+1)
	for i := range many {
		many[i] = fmt.Sprintf("kw %d", i)
	}
	if _, err := st.AddKeywords(u.ID, p.ID, many, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("over the keyword limit: %v", err)
	}
	if ks, _ := st.Keywords(u.ID, p.ID); len(ks) != 0 {
		t.Fatal("a rejected batch was partly stored")
	}
	for i := 2; i < MaxProjectsPerUser; i++ { // Default and Big exist
		if _, err := st.CreateProject(u.ID, fmt.Sprint("p", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateProject(u.ID, "one too many"); !errors.Is(err, ErrConflict) {
		t.Fatalf("over the project limit: %v", err)
	}
}
