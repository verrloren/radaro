package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/sources"
	"github.com/verrloren/radaro/internal/store"
)

type fakeSource struct {
	pages []sources.Page
	err   error
	calls int
}

func (f *fakeSource) FetchPage(_ context.Context, _ string, _ int, cursor string, _ time.Time) (sources.Page, error) {
	f.calls++
	if f.err != nil {
		return sources.Page{}, f.err
	}
	i := 0
	if cursor != "" {
		i = int(cursor[0] - '0')
	}
	if i >= len(f.pages) {
		return sources.Page{}, nil
	}
	return f.pages[i], nil
}

func fm(source, url, text string) model.Mention {
	m := model.Mention{Source: source, Query: "kestrel", Text: text, URL: model.Str(url), CreatedAt: time.Now().Add(-time.Hour)}
	m.Normalize()
	return m
}

func newTest(t *testing.T, fakes map[string]*fakeSource) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{PerSourceLimit: 50, SourceRetries: 0, RetryBackoff: 0.001, SentimentAnalyzer: "lexicon",
		AlertWindowHours: 24, AlertBaselineWindows: 7, AlertMinMentions: 5}
	for name := range fakes {
		cfg.Sources = append(cfg.Sources, name)
	}
	p := New(cfg, st)
	p.NewSource = func(name string, _ sources.Options) (sources.Source, error) {
		if f, ok := fakes[name]; ok {
			return f, nil
		}
		return nil, errors.New("unknown source")
	}
	return p, st
}

func TestTrackIsolatesFailuresAndAnalyzes(t *testing.T) {
	good := &fakeSource{pages: []sources.Page{{Mentions: []model.Mention{
		fm("hackernews", "https://1", "Kestrel pricing is awful"),
		fm("hackernews", "https://2", "Kestrel pricing is great"),
	}, NextCursor: "1"}}}
	bad := &fakeSource{err: errors.New("boom")}
	p, st := newTest(t, map[string]*fakeSource{"hackernews": good, "bluesky": bad})

	res, err := p.Track(context.Background(), " kestrel ", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Query != "kestrel" || res.Fetched != 2 || res.New != 2 {
		t.Fatalf("result %+v", res)
	}
	if res.Errors["bluesky"] != "boom" {
		t.Fatalf("errors %v", res.Errors)
	}
	if good.calls != 1 {
		t.Fatalf("first scan should fetch one page, fetched %d", good.calls)
	}
	ms, _ := st.Mentions(store.MentionFilter{Scope: store.Scope{Query: "kestrel"}})
	for _, m := range ms {
		if m.Sentiment == "" || m.Theme == nil || *m.Theme != "pricing" {
			t.Fatalf("mention not analyzed: %+v", m)
		}
	}
	state, _ := st.SourceState("kestrel", "hackernews")
	if state.BackfillCursor != "1" {
		t.Fatalf("first scan should seed backfill cursor, got %+v", state)
	}

	// Backfill follows the cursor to the end and marks history complete.
	good.pages = append(good.pages, sources.Page{Mentions: []model.Mention{fm("hackernews", "https://3", "older")}})
	res, err = p.Track(context.Background(), "kestrel", Options{Backfill: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.New != 1 || !res.BackfillComplete["hackernews"] {
		t.Fatalf("backfill %+v", res)
	}
	// A completed backfill is skipped next time.
	calls := good.calls
	if _, err := p.Track(context.Background(), "kestrel", Options{Backfill: true}); err != nil {
		t.Fatal(err)
	}
	if good.calls != calls {
		t.Fatal("completed backfill should not fetch again")
	}
}

func TestTrackDeliversNegativeAlertsOnce(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
	}))
	defer hook.Close()

	src := &fakeSource{pages: []sources.Page{{Mentions: []model.Mention{
		fm("hackernews", "https://1", "Kestrel is terrible and broken"),
		fm("hackernews", "https://2", "Kestrel is lovely"),
	}}}}
	p, _ := newTest(t, map[string]*fakeSource{"hackernews": src})
	p.Config.WebhookURL = hook.URL

	res, err := p.Track(context.Background(), "kestrel", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Alerted != 1 || res.AlertPending != 0 {
		t.Fatalf("alerts %+v", res)
	}
	if len(bodies) != 1 || bodies[0]["event"] != "radaro.negative_mentions" || bodies[0]["count"].(float64) != 1 {
		t.Fatalf("webhook bodies %v", bodies)
	}
	// Re-scanning the same data must not re-alert.
	if res, _ = p.Track(context.Background(), "kestrel", Options{}); res.Alerted != 0 || len(bodies) != 1 {
		t.Fatalf("re-alerted: %+v", res)
	}
}

func TestTrackValidation(t *testing.T) {
	p, _ := newTest(t, map[string]*fakeSource{"hackernews": {}})
	if _, err := p.Track(context.Background(), "  ", Options{}); err == nil {
		t.Fatal("empty query should fail")
	}
	if _, err := p.Track(context.Background(), "x", Options{Pages: 21}); err == nil {
		t.Fatal("pages > 20 should fail")
	}
}

func TestRetryDelayHonorsRetryAfter(t *testing.T) {
	if d := retryDelay(&sources.HTTPError{Status: 429, RetryAfter: "3"}, 1, 0); d != 3*time.Second {
		t.Fatalf("delay %v", d)
	}
	if d := retryDelay(errors.New("x"), 1, 2); d != 4*time.Second {
		t.Fatalf("backoff %v", d)
	}
}

func TestSourceOptionsUseConnectedAccounts(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{SourceOptions: sources.Options{RedditAccessToken: "env-token", MastodonAccessToken: "env-masto"}}
	o, err := SourceOptions(cfg, st)
	if err != nil || o.RedditAccessToken != "env-token" || o.MastodonAccessToken != "env-masto" {
		t.Fatalf("environment fallback %+v %v", o, err)
	}
	if _, err := st.SaveAccount("reddit", "me", publish.RedditCredentials{ClientID: "cid", ClientSecret: "sec", RefreshToken: "rt", Username: "me"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAccount("mastodon", "me@hachyderm.io", publish.MastodonCredentials{Instance: "https://hachyderm.io", AccessToken: "acct-tok"}); err != nil {
		t.Fatal(err)
	}
	o, err = SourceOptions(cfg, st)
	if err != nil || o.RedditRefreshToken != "rt" || o.RedditClientID != "cid" || o.RedditAccessToken != "" ||
		o.MastodonInstance != "https://hachyderm.io" || o.MastodonAccessToken != "acct-tok" {
		t.Fatalf("account options %+v %v", o, err)
	}
	if !sources.Configured("reddit", o) || !sources.Configured("mastodon", o) {
		t.Fatal("connected accounts should configure scanning")
	}
}
