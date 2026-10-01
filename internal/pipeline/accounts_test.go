package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/store"
)

const (
	platReddit   = "reddit"
	platMastodon = "mastodon"
	textDead     = "account dead"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func redditAccount(t *testing.T, st *store.Store, userID int64, handle string) *store.Account {
	t.Helper()
	acc, err := st.SaveAccount(userID, platReddit, handle, publish.RedditCredentials{ClientID: "c-" + handle, RefreshToken: "rt-" + handle})
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func pause(t *testing.T, st *store.Store, acc *store.Account) {
	t.Helper()
	yes := true
	if _, err := st.UpdateAccount(0, acc.ID, store.AccountUpdate{Paused: &yes}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceOptionsSkipDeadAndPausedAccounts(t *testing.T) {
	st := openStore(t)
	dead := redditAccount(t, st, 0, "dead")
	paused := redditAccount(t, st, 0, "paused")
	redditAccount(t, st, 0, "live")
	if _, err := st.SetAccountStatus(dead.ID, store.AccountSuspended, "suspended", time.Time{}); err != nil {
		t.Fatal(err)
	}
	pause(t, st, paused)
	o, err := SourceOptions(&config.Config{}, st, 0, 0)
	if err != nil || o.RedditRefreshToken != "rt-live" {
		t.Fatalf("scan must use the first live, unpaused account: %+v %v", o, err)
	}
}

// The project's pool comes first; a pooled account that cannot scan gives
// way to the user's next healthy one.
func TestSourceOptionsPreferThePool(t *testing.T) {
	st := openStore(t)
	u, err := st.CreateUser("a@example.com", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.CreateProject(u.ID, "Launch")
	redditAccount(t, st, u.ID, "first")
	pooled := redditAccount(t, st, u.ID, "pooled")
	if _, err := st.BindAccount(u.ID, p.ID, pooled.ID); err != nil {
		t.Fatal(err)
	}
	if o, err := SourceOptions(&config.Config{}, st, u.ID, p.ID); err != nil || o.RedditRefreshToken != "rt-pooled" {
		t.Fatalf("the pooled account scans: %+v %v", o, err)
	}
	if o, _ := SourceOptions(&config.Config{}, st, u.ID, 0); o.RedditRefreshToken != "rt-first" {
		t.Fatalf("without a project, the user's first account: %+v", o)
	}
	pause(t, st, pooled)
	if o, _ := SourceOptions(&config.Config{}, st, u.ID, p.ID); o.RedditRefreshToken != "rt-first" {
		t.Fatalf("a paused pooled account falls back to the user's live one: %+v", o)
	}
	other, _ := st.CreateUser("b@example.com", "h", true)
	if _, err := SourceOptions(&config.Config{}, st, other.ID, p.ID); err == nil {
		t.Fatal("another user's project must not be read")
	}
}

func TestAccountNotifier(t *testing.T) {
	if AccountNotifier(&config.Config{}) != nil {
		t.Fatal("no transports, no notifier")
	}
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	notify := AccountNotifier(&config.Config{WebhookURL: srv.URL})
	if notify == nil {
		t.Fatal("webhook configured, notifier missing")
	}
	if err := notify(context.Background(), textDead, map[string]any{"event": "radaro.account_suspended"}); err != nil {
		t.Fatal(err)
	}
	if got["text"] != textDead || got["event"] != "radaro.account_suspended" {
		t.Fatalf("payload %v", got)
	}
}

func TestAccountNotifierReportsFailedTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	notify := AccountNotifier(&config.Config{WebhookURL: srv.URL})
	if err := notify(context.Background(), textDead, map[string]any{}); err == nil {
		t.Fatal("a failed delivery must be reported")
	}
}

// Accounts whose credentials cannot scan (a Reddit account without a refresh
// token, a Mastodon account without a token) leave the configuration alone.
func TestSourceOptionsSkipIncompleteCredentials(t *testing.T) {
	st := openStore(t)
	_, _ = st.SaveAccount(0, platReddit, "half", publish.RedditCredentials{ClientID: "c1"})
	_, _ = st.SaveAccount(0, platMastodon, "none", publish.MastodonCredentials{Instance: "https://m.example"})
	_, _ = st.SaveAccount(0, platMastodon, "ok", publish.MastodonCredentials{Instance: "https://ok.example", AccessToken: "tok"})
	cfg := &config.Config{}
	cfg.SourceOptions.RedditClientID = "env-client"
	o, err := SourceOptions(cfg, st, 0, 0)
	if err != nil || o.RedditClientID != "env-client" || o.MastodonInstance != "https://ok.example" || o.MastodonAccessToken != "tok" {
		t.Fatalf("options %+v %v", o, err)
	}
}

func TestSourceOptionsStoreFailure(t *testing.T) {
	st := openStore(t)
	st.Close()
	if _, err := SourceOptions(&config.Config{}, st, 0, 0); err == nil {
		t.Fatal("a closed store is an error")
	}
}

func TestBrowserAccountPoolKeepsItsOwnProxyAndCookies(t *testing.T) {
	st := openStore(t)
	u, _ := st.CreateUser("browser@example.com", "h", true)
	p, _ := st.CreateProject(u.ID, "Browser")
	oauth := redditAccount(t, st, u.ID, "oauth")
	browser, err := st.SaveAccount(u.ID, platReddit, "browser", publish.RedditCredentials{Browser: &redditbrowser.Credentials{
		Username: "browser", Proxy: "socks5://proxy.example:1080", Cookies: []*network.CookieParam{{Name: "reddit_session", Value: "browser-cookie", Domain: ".reddit.com"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.BindAccount(u.ID, p.ID, browser.ID); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.SourceOptions.RedditAccessToken = "environment-token"
	o, err := SourceOptions(cfg, st, u.ID, p.ID)
	if err != nil || o.RedditBrowser == nil {
		t.Fatalf("pooled browser account not selected: %v", err)
	}
	if o.RedditBrowser.Proxy != "socks5://proxy.example:1080" || o.RedditBrowser.Cookies[0].Value != "browser-cookie" || o.RedditAccessToken != "" || o.RedditClientID != "" || o.RedditRefreshToken != "" {
		t.Fatal("account proxy, cookies or authentication mode mixed with another account")
	}
	pause(t, st, browser)
	o, err = SourceOptions(cfg, st, u.ID, p.ID)
	if err != nil || o.RedditBrowser != nil || o.RedditRefreshToken != "rt-"+oauth.Handle {
		t.Fatal("fallback account retained the browser authentication mode")
	}
}
