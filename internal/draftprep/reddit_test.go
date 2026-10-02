package draftprep

import (
	"context"
	"errors"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/store"
	"strings"
	"testing"
)

type fakeProvider struct {
	body, prompt, system string
	calls                int
}

func TestPromotionUnsuitabilityDoesNotBecomeAComment(t *testing.T) {
	p := &fakeProvider{body: unsuitablePrefix + " No Self-Advertising prohibits promotion of software projects. "}
	body, err := Reddit(context.Background(), p, store.ReplySettings{Brief: "Promote my open-source project"}, redditbrowser.PostDetails{CanReply: true})
	var unsuitable *UnsuitablePromotionError
	if body != "" || !errors.As(err, &unsuitable) || unsuitable.Reason != "No Self-Advertising prohibits promotion of software projects." {
		t.Fatalf("unsuitable promotion became a comment: %q %v", body, err)
	}
	if !strings.Contains(p.system, "Do not silently replace") || strings.Contains(p.system, "If the project brief is empty") {
		t.Fatal("promotion request can still be silently ignored")
	}
	p.body = unsuitablePrefix + strings.Repeat("界", 500)
	_, err = Reddit(context.Background(), p, store.ReplySettings{}, redditbrowser.PostDetails{CanReply: true})
	if !errors.As(err, &unsuitable) || len([]rune(unsuitable.Reason)) != 400 {
		t.Fatal("unbounded explanation")
	}
	p.body = unsuitablePrefix
	_, err = Reddit(context.Background(), p, store.ReplySettings{}, redditbrowser.PostDetails{CanReply: true})
	if !errors.As(err, &unsuitable) || unsuitable.Reason == "" {
		t.Fatal("missing explanation")
	}
}

func (p *fakeProvider) Name() string    { return "test" }
func (p *fakeProvider) Available() bool { return true }
func (p *fakeProvider) Complete(_ context.Context, prompt, system string, _ int) (string, error) {
	p.prompt = prompt
	p.system = system
	p.calls++
	return p.body, nil
}

func TestRedditDraftContextAndValidation(t *testing.T) {
	p := &fakeProvider{body: " Useful reply "}
	post := redditbrowser.PostDetails{Body: strings.Repeat("界", 30000), CanReply: true, Replies: []redditbrowser.Comment{{Body: "Existing answer"}}, Rules: []redditbrowser.Rule{{Description: "No spam"}}}
	body, err := Reddit(context.Background(), p, store.ReplySettings{Brief: "Verified facts", Instructions: "Use my comparison and ask a question"}, post)
	if err != nil || body != "Useful reply" || !strings.Contains(p.prompt, "Verified facts") || !strings.Contains(p.prompt, "Existing answer") || !strings.Contains(p.prompt, "No spam") || !strings.Contains(p.system, "untrusted") {
		t.Fatalf("draft %q %v", body, err)
	}
	if strings.Count(p.prompt, "界") != 12000 || len([]rune(post.Body)) != 30000 {
		t.Fatal("unbounded context or caller mutated")
	}
	if !strings.Contains(p.prompt, "Use my comparison and ask a question") || !strings.Contains(p.system, "project.instructions") {
		t.Fatal("user comment instructions were not passed to the model")
	}
	for _, bad := range []string{"", strings.Repeat("x", 10001)} {
		p.body = bad
		if _, err := Reddit(context.Background(), p, store.ReplySettings{}, post); err == nil {
			t.Fatal("invalid generated body accepted")
		}
	}
	calls := p.calls
	post.CanReply = false
	if _, err := Reddit(context.Background(), p, store.ReplySettings{}, post); err == nil || p.calls != calls {
		t.Fatal("generated for closed post")
	}
}
