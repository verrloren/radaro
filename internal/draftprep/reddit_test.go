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
	p := &fakeProvider{body: unsuitablePrefix + " The supplied project facts do not establish a connection to this topic. "}
	body, err := Reddit(context.Background(), p, store.ReplySettings{Brief: "Promote my open-source project"}, redditbrowser.PostDetails{CanReply: true})
	var unsuitable *UnsuitablePromotionError
	if body != "" || !errors.As(err, &unsuitable) || unsuitable.Reason != "The supplied project facts do not establish a connection to this topic." {
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

func TestPromotionDraftKeepsCommunityRulesForHumanReview(t *testing.T) {
	p := &fakeProvider{body: "Check how the launcher differs. AGI OS is another open-source Arch-based project with AI in the same ecosystem."}
	post := redditbrowser.PostDetails{Title: "Arch terminal question", CanReply: true, Rules: []redditbrowser.Rule{{Name: "No Self-Advertising", Description: "Do not advertise your own software projects."}}}
	settings := store.ReplySettings{Brief: "Promote AGI OS, an open-source Arch-based project with AI. No links."}
	body, err := Reddit(context.Background(), p, settings, post)
	if err != nil || body != p.body || !strings.Contains(p.prompt, "No Self-Advertising") || !strings.Contains(p.prompt, settings.Brief) {
		t.Fatalf("rule warning prevented a private draft: %q %v", body, err)
	}
	if !strings.Contains(p.system, "not an automatic veto") || !strings.Contains(p.system, "Do not invent") {
		t.Fatal("drafting and human rule review were not separated")
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
