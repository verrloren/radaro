package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
)

func TestImportAccounts(t *testing.T) {
	srv, st := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	_ = signedIn(t, srv, "owner@example.com")
	u, err := st.UserByEmail("owner@example.com")
	if err != nil || u == nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := st.SaveAccount(u.ID, "devto", "existing", publish.DevtoCredentials{APIKey: "old-key"}); err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	srv.connect = func(ctx context.Context, platform string, in publish.ConnectInput) (string, any, error) {
		n := active.Add(1)
		for n > peak.Load() && !peak.CompareAndSwap(peak.Load(), n) {
		}
		defer active.Add(-1)
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if in.Secret == "leaky-key" {
			return "", nil, errors.New("upstream echoed leaky-key")
		}
		return in.Handle, publish.DevtoCredentials{APIKey: in.Secret}, nil
	}
	input := "platform,handle,secret,instance\n" +
		"devto,existing,new-key,\n" +
		"devto,new,a-key,\n" +
		"devto,other,b-key,\n" +
		"devto,failed,leaky-key,\n" +
		"reddit,r,reddit-key,\n" +
		"devto,secret-in-handle,secret-in-handle,\n"
	body, _ := json.Marshal(map[string]string{"text": input})
	req := httptest.NewRequest("POST", "/api/accounts/import", strings.NewReader(string(body)))
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, u.ID))
	rec := httptest.NewRecorder()
	srv.importAccounts(rec, req)
	var got accountImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || got.Added != 3 || got.Updated != 1 || got.Errors != 2 || len(got.Results) != 6 {
		t.Fatalf("import %d: %+v", rec.Code, got)
	}
	if got.Results[0].Status != "updated" || got.Results[0].Row != 2 || got.Results[1].Status != "added" ||
		got.Results[3].Status != "error" || !strings.Contains(got.Results[4].Error, "Add another Reddit account") {
		t.Fatalf("results: %+v", got.Results)
	}
	if peak.Load() > 3 || peak.Load() < 2 {
		t.Fatalf("peak concurrent checks: %d", peak.Load())
	}
	for _, secret := range []string{"new-key", "a-key", "b-key", "leaky-key", "reddit-key", "secret-in-handle"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("response exposed a secret: %s", rec.Body)
		}
	}
	accs, err := st.Accounts(u.ID, "devto")
	if err != nil || len(accs) != 4 {
		t.Fatalf("accounts: %d, %v", len(accs), err)
	}
	activity, err := st.Activities(u.ID, 10)
	if err != nil || len(activity) < 3 || activity[0].Action != "account.imported" {
		t.Fatalf("activity: %+v, %v", activity, err)
	}
	other, err := st.CreateUser("other@example.com", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := st.Accounts(other.ID, "")
	if len(foreign) != 0 {
		t.Fatalf("other user's accounts: %+v", foreign)
	}
}

func TestImportAccountsInvalidInput(t *testing.T) {
	srv, _ := newTestServer(t, &config.Config{Sources: []string{"hackernews"}})
	h := signedIn(t, srv, "owner@example.com")
	if rec := do(srv.Handler(), "POST", "/api/accounts/import", `{"text":"devto,,key"}`); rec.Code != 401 {
		t.Fatalf("import without a session: %d", rec.Code)
	}
	for _, text := range []string{"", "[]", "not,csv,\"unclosed", "platform,secret"} {
		body, _ := json.Marshal(map[string]string{"text": text})
		rec := do(h, "POST", "/api/accounts/import", string(body))
		if rec.Code != 422 {
			t.Fatalf("input %q: %d %s", text, rec.Code, rec.Body)
		}
	}
}
