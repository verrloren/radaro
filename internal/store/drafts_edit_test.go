package store

import (
	"errors"
	"testing"
	"time"
)

func TestSetDraftAccountAndPublishedDrafts(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	acc := saveAccount(t, st, owner.ID, platBluesky, "me.bsky.social")
	uid := owner.ID
	d, err := st.CreateDraft(NewDraft{UserID: uid, Platform: platBluesky, Kind: kindPost, Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if d, err = st.SetDraftAccount(uid, d.ID, &acc.ID); err != nil || d.AccountID == nil || *d.AccountID != acc.ID {
		t.Fatalf("pin %+v %v", d, err)
	}
	if d, err = st.SetDraftAccount(uid, d.ID, nil); err != nil || d.AccountID != nil {
		t.Fatalf("auto %+v %v", d, err)
	}
	if _, err := st.SetDraftAccount(uid, 999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing draft: %v", err)
	}

	_, _ = st.SetDraftAccount(uid, d.ID, &acc.ID)
	_, _ = st.ApproveDraft(uid, d.ID)
	_, _ = st.BeginPublish(uid, d.ID)
	if _, err := st.FinishPublish(d.ID, "at://1", "https://bsky.app/1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDraftAccount(uid, d.ID, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("published draft changed account: %v", err)
	}

	got, err := st.PublishedDrafts(uid, platBluesky, time.Now().Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].ID != d.ID {
		t.Fatalf("published %v %v", got, err)
	}
	if got, _ := st.PublishedDrafts(uid, platBluesky, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("since filter %v", got)
	}
	if got, _ := st.PublishedDrafts(uid, platReddit, time.Now().Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("platform filter %v", got)
	}
}

func TestSetDraftAccountIsPerUser(t *testing.T) {
	st := openTest(t)
	alice, bob := newUser(t, st, emailA), newUser(t, st, emailB)
	aliceAcc := saveAccount(t, st, alice.ID, platBluesky, "alice")
	bobAcc := saveAccount(t, st, bob.ID, platBluesky, "bob")
	aliceMasto := saveAccount(t, st, alice.ID, platMastodon, "alice")
	aliceDraft, _ := st.CreateDraft(NewDraft{UserID: alice.ID, Platform: platBluesky, Kind: kindPost, Body: "a"})
	bobDraft, _ := st.CreateDraft(NewDraft{UserID: bob.ID, Platform: platBluesky, Kind: kindPost, Body: "b"})
	for name, c := range map[string]struct {
		user, draft int64
		account     *int64
	}{
		"another user's draft":        {bob.ID, aliceDraft.ID, &bobAcc.ID},
		"another user's draft, unpin": {bob.ID, aliceDraft.ID, nil},
		"another user's account":      {bob.ID, bobDraft.ID, &aliceAcc.ID},
		"a missing account":           {alice.ID, aliceDraft.ID, new(int64)},
	} {
		if _, err := st.SetDraftAccount(c.user, c.draft, c.account); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := st.SetDraftAccount(alice.ID, aliceDraft.ID, &aliceMasto.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("an account of another platform: %v", err)
	}
	if got, _ := st.Draft(alice.ID, aliceDraft.ID); got.AccountID != nil {
		t.Fatalf("A's draft was pinned: %+v", got)
	}
	d := publishDraft(t, st, NewDraft{UserID: alice.ID, Platform: platBluesky, Body: "pub"}, aliceAcc.ID, "https://bsky.app/2")
	if got, _ := st.PublishedDrafts(bob.ID, platBluesky, time.Now().Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("B lists A's published drafts: %+v", got)
	}
	if got, _ := st.PublishedDrafts(alice.ID, platBluesky, time.Now().Add(-time.Hour)); len(got) != 1 || got[0].ID != d.ID {
		t.Fatalf("A's published drafts: %+v", got)
	}
}
