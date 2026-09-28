package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var v map[string]any
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	return v
}

func TestBlueskyReplyWithFacets(t *testing.T) {
	var record map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			w.Write([]byte(`{"accessJwt":"jwt","did":"did:plc:me","handle":"me.bsky.social"}`))
		case "/xrpc/com.atproto.identity.resolveHandle":
			if r.URL.Query().Get("handle") != "alice.bsky.social" {
				t.Errorf("resolved %q", r.URL.Query().Get("handle"))
			}
			w.Write([]byte(`{"did":"did:plc:alice"}`))
		case "/xrpc/app.bsky.feed.getPosts":
			if got := r.URL.Query().Get("uris"); got != "at://did:plc:alice/app.bsky.feed.post/3abc" {
				t.Errorf("uris %q", got)
			}
			w.Write([]byte(`{"posts":[{"uri":"at://did:plc:alice/app.bsky.feed.post/3abc","cid":"cidP",
				"record":{"reply":{"root":{"uri":"at://did:plc:bob/app.bsky.feed.post/root","cid":"cidR"}}}}]}`))
		case "/xrpc/com.atproto.repo.createRecord":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				t.Error("missing session token")
			}
			record = readJSON(t, r)["record"].(map[string]any)
			w.Write([]byte(`{"uri":"at://did:plc:me/app.bsky.feed.post/3new","cid":"c"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	b := &Bluesky{Creds: BlueskyCredentials{Service: srv.URL, Identifier: "me", AppPassword: "pw"}}
	text := "Привет! See https://example.com/x. #golang"
	res, err := b.Publish(context.Background(), Post{Kind: "reply", Body: text, ReplyTo: "https://bsky.app/profile/alice.bsky.social/post/3abc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://bsky.app/profile/me.bsky.social/post/3new" {
		t.Fatalf("url %q", res.URL)
	}
	reply := record["reply"].(map[string]any)
	if reply["root"].(map[string]any)["cid"] != "cidR" || reply["parent"].(map[string]any)["cid"] != "cidP" {
		t.Fatalf("reply refs %v", reply)
	}
	facets := record["facets"].([]any)
	if len(facets) != 2 {
		t.Fatalf("facets %v", facets)
	}
	link := facets[0].(map[string]any)
	idx := link["index"].(map[string]any)
	start, end := int(idx["byteStart"].(float64)), int(idx["byteEnd"].(float64))
	if text[start:end] != "https://example.com/x" {
		t.Fatalf("link facet covers %q (trailing dot must be excluded, offsets are UTF-8 bytes)", text[start:end])
	}
	tag := facets[1].(map[string]any)["features"].([]any)[0].(map[string]any)
	if tag["tag"] != "golang" {
		t.Fatalf("tag facet %v", tag)
	}
}

func TestMastodonResolvesRemoteStatus(t *testing.T) {
	var form url.Values
	var idem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/search":
			if r.URL.Query().Get("resolve") != "true" {
				t.Error("search must resolve remote statuses")
			}
			w.Write([]byte(`{"statuses":[{"id":"42"}]}`))
		case "/api/v1/statuses":
			raw, _ := io.ReadAll(r.Body)
			form, _ = url.ParseQuery(string(raw))
			idem = r.Header.Get("Idempotency-Key")
			w.Write([]byte(`{"id":"99","url":"https://m.example/@me/99"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	m := &Mastodon{Creds: MastodonCredentials{Instance: srv.URL, AccessToken: "t"}}
	res, err := m.Publish(context.Background(), Post{Kind: "reply", Body: "hi", ReplyTo: "https://other.example/@a/1", IdempotencyKey: "radaro-draft-7"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RemoteID != "99" || form.Get("in_reply_to_id") != "42" || form.Get("visibility") != "public" || idem != "radaro-draft-7" {
		t.Fatalf("res %+v form %v idem %q", res, form, idem)
	}
}

func TestDevtoArticleAndMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/articles":
			a := readJSON(t, r)["article"].(map[string]any)
			if a["title"] != "Hello" || a["published"] != true || len(a["tags"].([]any)) != 2 {
				t.Errorf("article %v", a)
			}
			w.Write([]byte(`{"id":123,"url":"https://dev.to/me/hello"}`))
		case "/articles/me/published":
			w.Write([]byte(`[{"id":123,"page_views_count":50,"public_reactions_count":4,"comments_count":2}]`))
		}
	}))
	defer srv.Close()
	old := devtoAPI
	devtoAPI = srv.URL
	defer func() { devtoAPI = old }()

	d := &Devto{Creds: DevtoCredentials{APIKey: "k"}}
	res, err := d.Publish(context.Background(), Post{Kind: "post", Title: "Hello", Body: "# hi", Community: "go, #CLI"})
	if err != nil || res.RemoteID != "123" {
		t.Fatalf("publish %+v %v", res, err)
	}
	m, err := d.Metrics(context.Background(), "123")
	if err != nil || m["views"] != int64(50) {
		t.Fatalf("metrics %v %v", m, err)
	}
	if _, err := d.Publish(context.Background(), Post{Kind: "reply", Body: "x"}); err == nil {
		t.Fatal("dev.to replies must be refused")
	}
}

func TestRedditSubmitCommentAndErrors(t *testing.T) {
	tokenCalls := 0
	var submitted url.Values
	rateLimited := false
	www := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt" {
			t.Errorf("token form %v", form)
		}
		w.Write([]byte(`{"access_token":"at"}`))
	}))
	defer www.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" || !strings.HasPrefix(r.Header.Get("User-Agent"), "cli:radaro:") {
			t.Errorf("headers %v", r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		switch r.URL.Path {
		case "/api/submit":
			submitted = form
			if rateLimited {
				w.Write([]byte(`{"json":{"errors":[["RATELIMIT","you are doing that too much","ratelimit"]]}}`))
				return
			}
			w.Write([]byte(`{"json":{"errors":[],"data":{"name":"t3_new","url":"https://www.reddit.com/r/golang/comments/new/x/"}}}`))
		case "/api/comment":
			if form.Get("thing_id") != "t1_c2" {
				t.Errorf("thing_id %q", form.Get("thing_id"))
			}
			w.Write([]byte(`{"json":{"errors":[],"data":{"things":[{"data":{"name":"t1_mine","permalink":"/r/golang/comments/p1/x/mine/"}}]}}}`))
		case "/api/info":
			w.Write([]byte(`{"data":{"children":[{"data":{"score":17,"num_comments":3,"upvote_ratio":0.93}}]}}`))
		}
	}))
	defer api.Close()
	oldW, oldA := redditWWW, redditOAuth
	redditWWW, redditOAuth = www.URL, api.URL
	defer func() { redditWWW, redditOAuth = oldW, oldA }()

	r := &Reddit{Creds: RedditCredentials{ClientID: "id", ClientSecret: "s", RefreshToken: "rt", Username: "me"}, Version: "test"}
	res, err := r.Publish(context.Background(), Post{Kind: "post", Community: "/r/golang/", Title: "T", Body: "B"})
	if err != nil || res.RemoteID != "t3_new" || submitted.Get("sr") != "golang" || submitted.Get("kind") != "self" {
		t.Fatalf("submit %+v %v %v", res, err, submitted)
	}
	res, err = r.Publish(context.Background(), Post{Kind: "reply", Body: "B", ReplyTo: "https://www.reddit.com/r/golang/comments/p1/some_title/c2/"})
	if err != nil || res.URL != redditWWW+"/r/golang/comments/p1/x/mine/" {
		t.Fatalf("comment %+v %v", res, err)
	}
	m, err := r.Metrics(context.Background(), "t3_new")
	if err != nil || m["score"] != int64(17) || m["comments"] != int64(3) {
		t.Fatalf("metrics %v %v", m, err)
	}
	rateLimited = true
	if _, err := r.Publish(context.Background(), Post{Kind: "post", Community: "golang", Title: "T", Body: "B"}); err == nil ||
		!strings.Contains(err.Error(), "RATELIMIT") {
		t.Fatalf("a 200 with errors must fail: %v", err)
	}
	if tokenCalls != 1 {
		t.Fatalf("access token minted %d times", tokenCalls)
	}
}

func TestRedditThingID(t *testing.T) {
	cases := map[string]string{
		"https://www.reddit.com/r/linux/comments/1abc/title/":           "t3_1abc",
		"https://old.reddit.com/r/linux/comments/1abc/title/kx9/":       "t1_kx9",
		"https://www.reddit.com/r/linux/comments/1abc/title/kx9/?ctx=3": "t1_kx9",
		"t3_zz": "t3_zz",
	}
	for in, want := range cases {
		if got, err := RedditThingID(in); err != nil || got != want {
			t.Errorf("%s → %q, %v (want %s)", in, got, err, want)
		}
	}
	if _, err := RedditThingID("https://example.com"); err == nil {
		t.Error("non-reddit link accepted")
	}
}

func TestValidate(t *testing.T) {
	reddit, _ := LookupPlatform("reddit")
	bsky, _ := LookupPlatform("bluesky")
	devto, _ := LookupPlatform("devto")
	if reddit.Validate(Post{Kind: "post", Title: "T", Body: "B"}) == nil {
		t.Error("reddit post without subreddit accepted")
	}
	if bsky.Validate(Post{Kind: "post", Body: strings.Repeat("я", 301)}) == nil {
		t.Error("bluesky post over 300 chars accepted")
	}
	if bsky.Validate(Post{Kind: "post", Body: strings.Repeat("я", 300)}) != nil {
		t.Error("300 characters is allowed")
	}
	if devto.Validate(Post{Kind: "reply", Body: "x", ReplyTo: "u"}) == nil {
		t.Error("dev.to reply accepted")
	}
}
