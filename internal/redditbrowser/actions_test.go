package redditbrowser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

func TestWebsiteSearchPublishingAndCommentMetrics(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	var mu sync.Mutex
	var submitted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/record" {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			submitted = append(submitted, string(body))
			mu.Unlock()
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<div id="header-bottom-right"><span class="user"><a href="/user/alice">alice</a></span></div>`)
		switch {
		case r.URL.Path == "/search":
			io.WriteString(w, `<div class="search-result-listing"><div class="search-result-link"><a class="search-title">Found post</a><div class="search-result-body">Body</div><a class="search-author">bob</a><a class="search-comments" href="/r/go/comments/abc/title/">comments</a><time datetime="2026-09-30T12:00:00Z"></time><span class="search-score">12 points</span></div></div><span class="nav-buttons"><a href="/search?after=t3_abc">next</a></span>`)
		case strings.HasSuffix(r.URL.Path, "/submit"):
			io.WriteString(w, `<input name="title"><textarea name="text"></textarea><button name="submit" onclick="fetch('/record',{method:'POST',body:document.querySelector('[name=title]').value+'|'+document.querySelector('textarea').value}).then(()=>location.href='/r/go/comments/new/title/')">Submit</button>`)
		case strings.Contains(r.URL.Path, "/comments/") || strings.HasPrefix(r.URL.Path, "/by_id/"):
			io.WriteString(w, `<div class="thing link" data-fullname="t3_abc"><span class="score unvoted" title="99">99</span><a class="comments">15 comments</a></div><div class="thing comment id-t1_def" data-fullname="t1_def"><div class="entry"><a class="author">bob</a><span class="score unvoted">-2 points</span><div class="usertext-body">[removed]</div><ul class="buttons"><li class="reply-button"><a href="javascript:void(0)" onclick="this.closest('.entry').insertAdjacentHTML('beforeend',document.querySelector('#template').innerHTML)">reply</a></li></ul></div></div><div class="commentarea"><form class="usertext" onsubmit="return false"><textarea name="text"></textarea><button class="save" onclick="send(this)">Save</button></form></div><template id="template"><form class="usertext" onsubmit="return false"><textarea name="text"></textarea><button class="save" onclick="send(this)">Save</button></form></template><script>function send(button){fetch('/record',{method:'POST',body:button.closest('form').querySelector('textarea').value}).then(()=>document.body.insertAdjacentHTML('beforeend','<div class="thing comment" data-fullname="t1_new"><div class="entry"><a class="author">alice</a><a class="bylink" href="/r/go/comments/abc/title/new/">permalink</a></div></div>'))}</script>`)
		}
	}))
	defer srv.Close()
	previous := site
	site = srv.URL
	defer func() { site = previous }()
	u, _ := url.Parse(site)
	c := Credentials{Username: "alice", Cookies: []*network.CookieParam{{Name: "reddit_session", Value: "fake", Domain: u.Hostname(), Path: "/"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	page, err := Search(ctx, c, "go", "", 10)
	if err != nil || len(page.Mentions) != 1 {
		t.Fatalf("website search: %v", err)
	}
	if page.Next != "t3_abc" || page.Mentions[0].CreatedAt.IsZero() || *page.Mentions[0].Score != 12 || !strings.HasPrefix(page.Mentions[0].URL, "https://www.reddit.com/") {
		t.Fatalf("search result: %+v", page)
	}
	metrics, err := Metrics(ctx, c, "https://www.reddit.com/r/go/comments/abc/title/def/")
	if err != nil || metrics["score"] != float64(-2) || metrics["removed"] != true {
		t.Fatalf("comment metrics selected the wrong entry: %v %v", metrics, err)
	}
	for _, post := range []Post{
		{Kind: "post", Community: "go", Title: "Title", Body: "Post body"},
		{Kind: "reply", ReplyTo: "https://www.reddit.com/r/go/comments/abc/title/", Body: "Post reply"},
		{Kind: "reply", ReplyTo: "t1_def", Body: "Comment reply"},
	} {
		result, err := Publish(ctx, c, post)
		if err != nil || !strings.Contains(result.URL, "/comments/") || result.RemoteID != result.URL {
			t.Fatalf("publication was not confirmed: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(submitted, ",") != "Title|Post body,Post reply,Comment reply" {
		t.Fatalf("unexpected submissions: %v", submitted)
	}
}

func TestLoggedOutPageIsNotAnAccount(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `<div id="header-bottom-right"><span class="user">Want to join? <a class="login-required" href="/login">Log in</a></span></div>`)
	}))
	defer srv.Close()
	previous := site
	site = srv.URL
	defer func() { site = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Open(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Navigate(ctx, site); err != nil {
		t.Fatal(err)
	}
	if _, err = s.identity(ctx); err != ErrSession {
		t.Fatalf("logged-out link accepted as a username: %v", err)
	}
}
