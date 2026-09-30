package store

import (
	"errors"
	"testing"
)

func TestProjectPoolIsPerUser(t *testing.T) {
	st := openTest(t)
	alice, bob := newUser(t, st, emailA), newUser(t, st, emailB)
	p, _ := st.CreateProject(alice.ID, "Launch")
	acc := saveAccount(t, st, alice.ID, platReddit, "alice")
	if _, err := st.BindAccount(alice.ID, p.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: %v", err)
	}
	if _, err := st.BindAccount(alice.ID, p.ID, acc.ID); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"pool":          second(st.ProjectPool(bob.ID, p.ID, platReddit)),
		"bindings":      second(st.ProjectBindings(bob.ID, p.ID)),
		"unbind":        second(st.UnbindAccount(bob.ID, p.ID, platReddit)),
		"unbind one":    second(st.UnbindProjectAccount(bob.ID, p.ID, acc.ID)),
		"bind own into": second(st.BindAccount(bob.ID, p.ID, saveAccount(t, st, bob.ID, platReddit, "bob").ID)),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("B %s on A's project: %v", name, err)
		}
	}
	if pool, _ := st.ProjectPool(alice.ID, p.ID, platReddit); len(pool) != 1 || pool[0].ID != acc.ID {
		t.Fatalf("A's pool changed: %+v", pool)
	}
	if pool, err := st.ProjectPool(alice.ID, p.ID, platDevto); err != nil || len(pool) != 0 {
		t.Fatalf("empty pool %+v %v", pool, err)
	}
	if ok, err := st.UnbindProjectAccount(alice.ID, p.ID, 999); ok || err != nil {
		t.Fatalf("unbinding an account not in the pool: %v %v", ok, err)
	}
	if ok, err := st.UnbindAccount(alice.ID, p.ID, platDevto); ok || err != nil {
		t.Fatalf("emptying an empty pool: %v %v", ok, err)
	}
}

func TestPublishAccountChoices(t *testing.T) {
	st := openTest(t)
	owner := newUser(t, st, emailA)
	p, _ := st.CreateProject(owner.ID, "Launch")
	draft := func(accountID int64) *Draft {
		d, err := st.CreateDraft(NewDraft{UserID: owner.ID, ProjectID: p.ID, Platform: platDevto, AccountID: accountID, Kind: kindPost, Title: "t", Body: "b"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if _, err := st.PublishAccount(owner.ID, draft(0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no account connected: %v", err)
	}
	only := saveAccount(t, st, owner.ID, platDevto, "only")
	if acc, err := st.PublishAccount(owner.ID, draft(0)); err != nil || acc.ID != only.ID {
		t.Fatalf("the user's only account: %+v %v", acc, err)
	}
	pinned := draft(only.ID)
	if acc, err := st.PublishAccount(owner.ID, pinned); err != nil || acc.ID != only.ID {
		t.Fatalf("the named account: %+v %v", acc, err)
	}
	st.DeleteAccount(owner.ID, only.ID)
	gone := int64(only.ID)
	pinned.AccountID = &gone // as loaded before the account was removed
	if _, err := st.PublishAccount(owner.ID, pinned); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed account: %v", err)
	}
	pooled := saveAccount(t, st, owner.ID, platDevto, "pooled")
	saveAccount(t, st, owner.ID, platDevto, "spare")
	if _, err := st.BindAccount(owner.ID, p.ID, pooled.ID); err != nil {
		t.Fatal(err)
	}
	if acc, err := st.PublishAccount(owner.ID, draft(0)); err != nil || acc.ID != pooled.ID {
		t.Fatalf("the only pooled account: %+v %v", acc, err)
	}
}
