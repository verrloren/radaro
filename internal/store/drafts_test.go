package store

import (
	"errors"
	"testing"
)

func TestDraftLifecycle(t *testing.T) {
	st := openTest(t)
	acc, err := st.SaveAccount(0, "bluesky", "me.bsky.social", map[string]string{"app_password": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := st.SaveAccount(0, "bluesky", "me.bsky.social", map[string]string{"app_password": "y"}); again.ID != acc.ID {
		t.Fatal("reconnecting must update the same account")
	}
	if _, err := st.CreateDraft(NewDraft{Platform: "bluesky", Kind: "reply", Body: "hi"}); err == nil {
		t.Fatal("reply without target accepted")
	}
	d, err := st.CreateDraft(NewDraft{Platform: "bluesky", AccountID: acc.ID, Kind: "post", Body: "hello", MentionID: "m1"})
	if err != nil || d.Status != DraftPending {
		t.Fatalf("create %+v %v", d, err)
	}
	if _, err := st.BeginPublish(0, d.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("unapproved draft must not publish")
	}
	if d, err = st.ApproveDraft(0, d.ID); err != nil || d.ApprovedAt == nil {
		t.Fatalf("approve %+v %v", d, err)
	}
	// Editing sends it back to review.
	body := "hello again"
	if d, err = st.EditDraft(0, d.ID, DraftEdit{Body: &body}); err != nil || d.Status != DraftPending || d.ApprovedAt != nil {
		t.Fatalf("edit %+v %v", d, err)
	}
	_, _ = st.ApproveDraft(0, d.ID)
	if _, err := st.BeginPublish(0, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginPublish(0, d.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("a draft must not be claimed twice")
	}
	if d, err = st.FinishPublish(d.ID, "", "", errors.New("HTTP 500")); err != nil || d.Status != DraftFailed || d.Error == nil {
		t.Fatalf("fail %+v %v", d, err)
	}
	_, _ = st.ApproveDraft(0, d.ID) // retry after a failure
	_, _ = st.BeginPublish(0, d.ID)
	if d, err = st.FinishPublish(d.ID, "at://x", "https://bsky.app/x", nil); err != nil || d.Status != DraftPublished || *d.RemoteURL != "https://bsky.app/x" {
		t.Fatalf("publish %+v %v", d, err)
	}
	if _, err := st.EditDraft(0, d.ID, DraftEdit{Body: &body}); !errors.Is(err, ErrConflict) {
		t.Fatal("published drafts are immutable")
	}
	if err := st.SaveMetrics(d.ID, map[string]any{"likes": 3}); err != nil {
		t.Fatal(err)
	}
	if d, _ = st.Draft(0, d.ID); d.Metrics["likes"] != float64(3) {
		t.Fatalf("metrics %v", d.Metrics)
	}
	drafted, _ := st.DraftedMentionIDs(0)
	if !drafted["m1"] {
		t.Fatal("drafted mention not tracked")
	}
	_ = st.LogActivity(0, "draft.published", d.ID, "x")
	if acts, _ := st.Activities(0, 10); len(acts) != 1 || *acts[0].DraftID != d.ID {
		t.Fatalf("activity %+v", acts)
	}
	// Removing the account keeps the draft.
	if ok, _ := st.DeleteAccount(0, acc.ID); !ok {
		t.Fatal("account not deleted")
	}
	if d, _ = st.Draft(0, d.ID); d == nil || d.AccountID != nil {
		t.Fatalf("draft after account removal %+v", d)
	}
}
