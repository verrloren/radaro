package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, target *string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	old := *target
	*target = srv.URL
	t.Cleanup(func() { *target = old; srv.Close() })
	return srv
}

func TestHackerNewsFiltersAndPaginates(t *testing.T) {
	var gotFilters string
	serve(t, &hackerNewsAPI, func(w http.ResponseWriter, r *http.Request) {
		gotFilters = r.URL.Query().Get("numericFilters")
		w.Write([]byte(`{"page":0,"nbPages":3,"hits":[
			{"objectID":"1","created_at_i":200,"title":"Show HN: Kestrel","author":"a","points":5},
			{"objectID":"2","created_at_i":150,"comment_text":"<p>I tried <b>kestrel</b> &amp; liked it</p>","author":"b"},
			{"objectID":"3","created_at_i":100,"comment_text":"unrelated","author":"c"}]}`))
	})
	page, err := (&HackerNews{}).FetchPage(context.Background(), "Kestrel", 50, "300", time.Unix(50, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Mentions) != 2 {
		t.Fatalf("got %d mentions, want 2 (query filter)", len(page.Mentions))
	}
	if page.Mentions[1].Text != "I tried kestrel & liked it" {
		t.Fatalf("html not stripped: %q", page.Mentions[1].Text)
	}
	if *page.Mentions[0].URL != "https://news.ycombinator.com/item?id=1" {
		t.Fatalf("url %q", *page.Mentions[0].URL)
	}
	if page.NextCursor != "100" {
		t.Fatalf("cursor %q, want oldest timestamp", page.NextCursor)
	}
	if gotFilters != "created_at_i<=300,created_at_i>50" {
		t.Fatalf("filters %q", gotFilters)
	}
}

func TestBlueskyCursorForbiddenEndsHistory(t *testing.T) {
	serve(t, &blueskyAPI, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"cursor":"next","posts":[{"uri":"at://did/app.bsky.feed.post/abc","author":{"handle":"x.bsky.social"},"record":{"text":"hi"},"indexedAt":"2026-09-01T10:00:00.000Z","likeCount":3}]}`))
	})
	src := &Bluesky{}
	p1, err := src.FetchPage(context.Background(), "hi", 10, "", time.Time{})
	if err != nil || len(p1.Mentions) != 1 || p1.NextCursor != "next" {
		t.Fatalf("page 1 %+v, %v", p1, err)
	}
	if *p1.Mentions[0].URL != "https://bsky.app/profile/x.bsky.social/post/abc" {
		t.Fatalf("url %q", *p1.Mentions[0].URL)
	}
	p2, err := src.FetchPage(context.Background(), "hi", 10, "next", time.Time{})
	if err != nil || len(p2.Mentions) != 0 || p2.NextCursor != "" {
		t.Fatalf("page 2 %+v, %v", p2, err)
	}
}

func TestRedditMintsTokenOnce(t *testing.T) {
	tokenCalls := 0
	serve(t, &redditTokenAPI, func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		if u, p, _ := r.BasicAuth(); u != "id" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"access_token":"tok"}`))
	})
	serve(t, &redditAPI, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":{"after":"t3_x","children":[{"data":{"author":"u","title":"T","selftext":"body","permalink":"/r/x/1","created_utc":1700000000.0,"score":12.0}}]}}`))
	})
	src := &Reddit{ClientID: "id", ClientSecret: "secret"}
	for range 2 {
		p, err := src.FetchPage(context.Background(), "q", 10, "", time.Time{})
		if err != nil || len(p.Mentions) != 1 || p.NextCursor != "t3_x" {
			t.Fatalf("page %+v, %v", p, err)
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("token minted %d times", tokenCalls)
	}
	if _, err := (&Reddit{}).FetchPage(context.Background(), "q", 10, "", time.Time{}); err == nil {
		t.Fatal("missing credentials should error")
	}
}

func TestRetryable(t *testing.T) {
	if !Retryable(&HTTPError{Status: 429}) || !Retryable(&HTTPError{Status: 503}) {
		t.Fatal("429/5xx must be retryable")
	}
	if Retryable(&HTTPError{Status: 404}) || Retryable(errors.New("x")) {
		t.Fatal("404 and plain errors are not retryable")
	}
}

func TestMatchingExcerptKeepsQueryVisible(t *testing.T) {
	text := strings.Repeat("a ", 600) + "NEEDLE" + strings.Repeat(" b", 600)
	got := matchingExcerpt(text, "needle", 800)
	if !strings.Contains(got, "NEEDLE") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("bad excerpt: %q…", got[:40])
	}
}

func TestXRecentStart(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if got := xRecentStart(now.Add(-time.Hour), now); got != "2026-09-28T11:00:00Z" {
		t.Fatalf("got %q", got)
	}
	if xRecentStart(now.Add(-8*24*time.Hour), now) != "" || xRecentStart(now.Add(-10*time.Second), now) != "" {
		t.Fatal("out-of-window boundaries must fall back to the default window")
	}
}

func TestRegistry(t *testing.T) {
	if len(All()) != 8 {
		t.Fatalf("want 8 sources, got %d", len(All()))
	}
	if !Configured("hackernews", Options{}) || Configured("reddit", Options{}) {
		t.Fatal("configured flags wrong")
	}
	if !Configured("reddit", Options{RedditAccessToken: "t"}) {
		t.Fatal("reddit token should configure it")
	}
	if _, err := New("nope", Options{}); err == nil {
		t.Fatal("unknown source should error")
	}
}

func TestMergeStoredSettings(t *testing.T) {
	env := Options{RedditClientID: "env-id", RedditClientSecret: "env-secret", RSSFeeds: []string{"https://a/feed"}}
	got := env.Merge(map[string]map[string]string{
		"reddit":  {"client_id": "ui-id", "client_secret": " "},
		"rss":     {"feeds": "https://b/feed\nhttps://c/feed, https://d/feed"},
		"youtube": {"api_key": "yt", "unknown": "ignored"},
		"bogus":   {"x": "y"},
	})
	if got.RedditClientID != "ui-id" || got.RedditClientSecret != "env-secret" {
		t.Fatalf("reddit %+v", got)
	}
	if strings.Join(got.RSSFeeds, " ") != "https://b/feed https://c/feed https://d/feed" || got.YouTubeAPIKey != "yt" {
		t.Fatalf("merged %+v", got)
	}
	if env.RedditClientID != "env-id" || len(env.RSSFeeds) != 1 {
		t.Fatal("merge mutated the environment options")
	}
	if !Configured("youtube", got) || Configured("x", got) {
		t.Fatal("configured after merge")
	}
	if got.Value("rss", "feeds") != "https://b/feed\nhttps://c/feed\nhttps://d/feed" {
		t.Fatalf("value %q", got.Value("rss", "feeds"))
	}
	for _, info := range All() {
		if info.NeedsConfig != (len(Fields(info.Name)) > 0) {
			t.Fatalf("%s: needs_config and fields disagree", info.Name)
		}
	}
}
