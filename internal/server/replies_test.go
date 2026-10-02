package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/llm"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/store"
)

type replyLLM struct {
	calls  int
	prompt string
	fail   bool
}

func (p *replyLLM) Name() string    { return "test" }
func (p *replyLLM) Available() bool { return true }
func (p *replyLLM) Complete(_ context.Context, prompt, system string, _ int) (string, error) {
	p.calls++
	p.prompt = prompt
	if p.fail {
		return "", errors.New("secret-provider-key")
	}
	return "A helpful reply", nil
}

func replyServer(t *testing.T) (*Server, *store.Store, http.Handler, string) {
	t.Helper()
	s, st := newTestServer(t, &config.Config{Registration: "open", LLM: llm.Settings{Provider: "openai", Model: "example"}})
	h := signedIn(t, s, "owner@example.com")
	m := &model.Mention{Source: "reddit", Query: "go", Title: model.Str("Question"), Text: "Original", URL: model.Str("https://www.reddit.com/r/go/comments/abc/question/"), CreatedAt: time.Now()}
	m.Normalize()
	if _, err := st.Upsert([]*model.Mention{m}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAccount(1, "reddit", "alice", publish.RedditCredentials{Browser: &redditbrowser.Credentials{Username: "alice", Proxy: "http://private-proxy:3128", Cookies: []*network.CookieParam{{Name: "reddit_session", Value: "private-cookie"}}}}); err != nil {
		t.Fatal(err)
	}
	s.loadRedditPost = func(_ context.Context, c redditbrowser.Credentials, target string) (redditbrowser.PostDetails, error) {
		if c.Username != "alice" || c.Proxy != "http://private-proxy:3128" {
			t.Fatal("wrong account or proxy")
		}
		return redditbrowser.PostDetails{URL: target, Title: "Question", Body: "Full post", Community: "go", Author: "bob", Score: model.Int(13), Comments: model.Int(47), CanReply: true, Replies: []redditbrowser.Comment{{Body: "Existing answer"}}, Rules: []redditbrowser.Rule{{Name: "Be helpful"}}}, nil
	}
	return s, st, h, m.ID
}

func TestReplyPreparationAndProjectOwnership(t *testing.T) {
	s, st, h, id := replyServer(t)
	other := signedIn(t, s, "other@example.com")
	provider := &replyLLM{}
	s.newLLM = func() (llm.Provider, error) { return provider, nil }
	path := "/api/mentions/" + id
	for _, action := range []string{"/reddit", "/reply"} {
		method := "GET"
		body := ""
		if action == "/reply" {
			method = "POST"
			body = `{"generate":true}`
		}
		if r := do(other, method, path+action, body); r.Code != 404 {
			t.Fatalf("foreign mention %d", r.Code)
		}
	}
	if r := do(h, "GET", path+"/reddit?project_id=2", ""); r.Code != 404 {
		t.Fatalf("foreign project %d", r.Code)
	}
	if r := do(h, "GET", path+"/reddit", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"comments":47`) {
		t.Fatalf("details %d %s", r.Code, r.Body)
	}
	if r := do(h, "PUT", "/api/projects/1/reply-settings", `{"brief":"Verified project description","language":"English","tone":"concise"}`); r.Code != 200 {
		t.Fatal(r.Body)
	}
	if r := do(other, "GET", "/api/projects/1/reply-settings", ""); r.Code != 404 {
		t.Fatalf("foreign settings %d", r.Code)
	}
	r := do(h, "POST", path+"/reply", `{"generate":true}`)
	var d store.Draft
	json.Unmarshal(r.Body.Bytes(), &d)
	if r.Code != 201 || d.Status != "draft" || d.Kind != "reply" || d.ProjectID == nil || *d.ProjectID != 1 || d.MentionID == nil || *d.MentionID != id {
		t.Fatalf("prepared %d %s", r.Code, r.Body)
	}
	if !strings.Contains(provider.prompt, "Verified project description") || !strings.Contains(provider.prompt, "Existing answer") || !strings.Contains(provider.prompt, "Be helpful") {
		t.Fatal("missing reply context")
	}
	if strings.Contains(r.Body.String(), "private-cookie") || strings.Contains(r.Body.String(), "private-proxy") {
		t.Fatal("credentials exposed")
	}
	r = do(h, "POST", path+"/reply", `{"generate":true}`)
	var same store.Draft
	json.Unmarshal(r.Body.Bytes(), &same)
	if r.Code != 200 || same.ID != d.ID || provider.calls != 1 {
		t.Fatalf("duplicate preparation %d %s", r.Code, r.Body)
	}
	if r := do(h, "POST", "/api/drafts/1/publish", ""); r.Code != 409 {
		t.Fatalf("unapproved draft published %d", r.Code)
	}
	if ds, _ := st.Drafts(1, "", 0); len(ds) != 1 {
		t.Fatalf("duplicate drafts %d", len(ds))
	}
}

func TestManualReplyAndLLMStatus(t *testing.T) {
	s, _, h, id := replyServer(t)
	if r := do(h, "GET", "/api/settings/llm", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"not_configured"`) {
		t.Fatalf("status %s", r.Body)
	}
	if r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true}`); r.Code != 409 {
		t.Fatalf("missing llm %d", r.Code)
	}
	if r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"body":"Manual reply"}`); r.Code != 201 {
		t.Fatalf("manual %d %s", r.Code, r.Body)
	}
	provider := &replyLLM{fail: true}
	s.newLLM = func() (llm.Provider, error) { return provider, nil }
	r := do(h, "POST", "/api/settings/llm/check", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"error"`) || strings.Contains(r.Body.String(), "secret-provider-key") {
		t.Fatalf("failed check %s", r.Body)
	}
	provider.fail = false
	r = do(h, "POST", "/api/settings/llm/check", "")
	if !strings.Contains(r.Body.String(), `"status":"connected"`) || strings.Contains(r.Body.String(), `"checked_at":null`) {
		t.Fatalf("connected %s", r.Body)
	}
	other := signedIn(t, s, "other@example.com")
	if r := do(other, "POST", "/api/settings/llm/check", ""); r.Code != 403 {
		t.Fatalf("nonadmin check %d", r.Code)
	}
}

func TestGenerationFailureDoesNotSaveOrExposeCredentials(t *testing.T) {
	s, st, h, id := replyServer(t)
	p := &replyLLM{fail: true}
	s.newLLM = func() (llm.Provider, error) { return p, nil }
	r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true}`)
	if r.Code != 502 || strings.Contains(r.Body.String(), "secret-provider-key") {
		t.Fatalf("unsafe error %d %s", r.Code, r.Body)
	}
	if ds, _ := st.Drafts(1, "", 0); len(ds) != 0 {
		t.Fatal("failed generation created a draft")
	}
	s.loadRedditPost = func(context.Context, redditbrowser.Credentials, string) (redditbrowser.PostDetails, error) {
		return redditbrowser.PostDetails{Locked: true}, nil
	}
	if r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true}`); r.Code != 422 || p.calls != 1 {
		t.Fatalf("locked post %d calls %d", r.Code, p.calls)
	}
}

type blockingReplyLLM struct {
	replyLLM
	started, release chan struct{}
}

func (p *blockingReplyLLM) Complete(ctx context.Context, prompt, system string, n int) (string, error) {
	close(p.started)
	select {
	case <-p.release:
		return p.replyLLM.Complete(ctx, prompt, system, n)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestConcurrentReplyClicksDoNotCreateDuplicates(t *testing.T) {
	s, st, h, id := replyServer(t)
	p := &blockingReplyLLM{started: make(chan struct{}), release: make(chan struct{})}
	s.newLLM = func() (llm.Provider, error) { return p, nil }
	done := make(chan int, 1)
	go func() { done <- do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true}`).Code }()
	<-p.started
	if r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true}`); r.Code != 409 {
		t.Fatalf("overlapping click %d", r.Code)
	}
	close(p.release)
	if code := <-done; code != 201 {
		t.Fatalf("first click %d", code)
	}
	if ds, _ := st.Drafts(1, "", 0); len(ds) != 1 {
		t.Fatalf("duplicates %d", len(ds))
	}
}
