package redditbrowser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

func TestReadPostDetailsAndRulesWithoutSubmitting(t *testing.T) {
	if Executable() == "" {
		t.Skip("Chromium not installed")
	}
	submissions := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			submissions++
		}
		io.WriteString(w, `<div id="header-bottom-right"><span class="user"><a href="/user/alice">alice</a></span></div>`)
		if strings.Contains(r.URL.Path, "/about/rules") {
			io.WriteString(w, `<div class="subreddit-rule-item"><div class="subreddit-rule-title">Be helpful</div><div class="subreddit-rule-description">No spam</div></div>`)
			return
		}
		io.WriteString(w, `<div class="thing link"><a class="title">Question</a><a class="subreddit">r/go</a><a class="author">bob</a><time datetime="2026-10-01T12:00:00Z"></time><span class="score unvoted" title="0">0 points</span><a class="comments">1,234 comments</a><span class="linkflairlabel">Help</span><div class="usertext-body">The full post body</div></div><div class="linkinfo"><div class="score">0 points (75% upvoted)</div></div><div class="commentarea"><form class="usertext"><textarea name="text"></textarea></form></div><div class="thing comment"><div class="entry"><a class="author">carol</a><span class="score">3 points</span><div class="usertext-body">Existing answer</div><a class="bylink" href="/r/go/comments/abc/question/def/">permalink</a></div></div>`)
	}))
	defer srv.Close()
	previous := site
	site = srv.URL
	defer func() { site = previous }()
	u, _ := url.Parse(site)
	c := Credentials{Username: "alice", Cookies: []*network.CookieParam{{Name: "reddit_session", Value: "fake", Domain: u.Hostname(), Path: "/"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	post, err := Details(ctx, c, "https://www.reddit.com/r/go/comments/abc/question/")
	if err != nil {
		t.Fatal(err)
	}
	if post.Title != "Question" || post.Body != "The full post body" || post.Score == nil || *post.Score != 0 || post.Comments == nil || *post.Comments != 1234 || post.UpvoteRatio == nil || *post.UpvoteRatio != .75 || !post.CanReply || post.CreatedAt == nil || post.Community != "go" {
		t.Fatalf("details %+v", post)
	}
	if len(post.Replies) != 1 || post.Replies[0].Body != "Existing answer" || len(post.Rules) != 1 || post.Rules[0].Description != "No spam" || submissions != 0 {
		t.Fatalf("context %+v submissions %d", post, submissions)
	}
}
