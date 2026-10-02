package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
	body   string
}

func (p *replyLLM) Name() string    { return "test" }
func (p *replyLLM) Available() bool { return true }
func (p *replyLLM) Complete(_ context.Context, prompt, system string, _ int) (string, error) {
	p.calls++
	p.prompt = prompt
	if p.fail {
		return "", errors.New("secret-provider-key")
	}
	if p.body != "" {
		return p.body, nil
	}
	return "A helpful reply", nil
}

func TestUnsuitablePromotionPreservesDraftAndLLMConnection(t *testing.T) {
	s, st, h, id := replyServer(t)
	p := &replyLLM{body: "RADARO_PROMOTION_UNSUITABLE: The project facts do not establish a truthful connection to this topic."}
	s.newLLM = func() (llm.Provider, error) { return p, nil }
	path := "/api/mentions/" + id + "/reply"
	r := do(h, "POST", path, `{"generate":true}`)
	if ds, _ := st.Drafts(1, "", 0); r.Code != 422 || len(ds) != 0 {
		t.Fatal("unsuitable promotion created a new draft")
	}
	r = do(h, "POST", path, `{"body":"Existing manually edited reply"}`)
	var old store.Draft
	json.Unmarshal(r.Body.Bytes(), &old)
	if r.Code != 201 {
		t.Fatal(r.Body)
	}
	oldPtr, err := st.ApproveDraft(1, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	r = do(h, "POST", path, `{"generate":true,"regenerate":true,"draft_id":`+strconv.FormatInt(old.ID, 10)+`}`)
	got, _ := st.Draft(1, old.ID)
	if r.Code != 422 || !strings.Contains(r.Body.String(), "truthful connection") || got.Body != oldPtr.Body || got.Status != oldPtr.Status || got.UpdatedAt != oldPtr.UpdatedAt {
		t.Fatalf("unsuitability changed draft: status=%d body=%s", r.Code, r.Body)
	}
	if s.llmView().Status != "connected" {
		t.Fatal("unsuitable post marked provider disconnected")
	}
	if ds, _ := st.Drafts(1, "", 0); len(ds) != 1 {
		t.Fatal("unsuitability created a draft")
	}
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
	if r := do(h, "PUT", "/api/projects/1/reply-settings", `{"brief":"Verified project description","instructions":"Base the reply on the supplied comparison","language":"English","tone":"concise"}`); r.Code != 200 {
		t.Fatal(r.Body)
	}
	if r := do(h, "GET", "/api/projects/1/reply-settings", ""); r.Code != 200 || !strings.Contains(r.Body.String(), "Base the reply on the supplied comparison") {
		t.Fatal("instructions were not saved")
	}
	if r := do(h, "PUT", "/api/projects/1/reply-settings", `{"instructions":"`+strings.Repeat("x", 8001)+`"}`); r.Code != 422 {
		t.Fatal("oversized instructions accepted")
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
	if !strings.Contains(provider.prompt, "Verified project description") || !strings.Contains(provider.prompt, "Base the reply on the supplied comparison") || !strings.Contains(provider.prompt, "Existing answer") || !strings.Contains(provider.prompt, "Be helpful") {
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

func TestRegenerateReplyUsesNewContextAndResetsApproval(t *testing.T) {
	s, st, h, id := replyServer(t)
	p := &replyLLM{}
	s.newLLM = func() (llm.Provider, error) { return p, nil }
	path := "/api/mentions/" + id + "/reply"
	r := do(h, "POST", path, `{"body":"Old manually edited text"}`)
	var old store.Draft
	json.Unmarshal(r.Body.Bytes(), &old)
	if r.Code != 201 {
		t.Fatal(r.Body)
	}
	if _, err := st.ApproveDraft(1, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReplySettings(1, 1, store.ReplySettings{Brief: "Our actual product", Instructions: "Discuss the provided use case"}); err != nil {
		t.Fatal(err)
	}
	r = do(h, "POST", path, `{"generate":true,"regenerate":true,"draft_id":`+strconv.FormatInt(old.ID, 10)+`}`)
	var got store.Draft
	json.Unmarshal(r.Body.Bytes(), &got)
	if r.Code != 200 || got.ID != old.ID || got.Status != store.DraftPending || got.ApprovedAt != nil || got.Body != "A helpful reply" || *got.ReplyTo != *old.ReplyTo {
		t.Fatalf("regeneration %d %s", r.Code, r.Body)
	}
	if !strings.Contains(p.prompt, "Our actual product") || !strings.Contains(p.prompt, "Discuss the provided use case") {
		t.Fatal("old context used")
	}
	p.fail = true
	r = do(h, "POST", path, `{"generate":true,"regenerate":true,"draft_id":`+strconv.FormatInt(old.ID, 10)+`}`)
	after, _ := st.Draft(1, old.ID)
	if r.Code != 502 || after.Body != got.Body || after.UpdatedAt != got.UpdatedAt {
		t.Fatal("failure changed draft")
	}
	if ds, _ := st.Drafts(1, "", 0); len(ds) != 1 {
		t.Fatal("regeneration created duplicate")
	}
	if _, err := st.ApproveDraft(1, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BeginPublish(1, old.ID); err != nil {
		t.Fatal(err)
	}
	calls := p.calls
	r = do(h, "POST", path, `{"generate":true,"regenerate":true,"draft_id":`+strconv.FormatInt(old.ID, 10)+`}`)
	if r.Code != 409 || p.calls != calls {
		t.Fatal("regenerated publishing draft")
	}
}

func TestRegenerationDoesNotOverwriteConcurrentEditsOrPublishClaims(t *testing.T) {
	for _, publishing := range []bool{false, true} {
		t.Run(strconv.FormatBool(publishing), func(t *testing.T) {
			s, st, h, id := replyServer(t)
			r := do(h, "POST", "/api/mentions/"+id+"/reply", `{"body":"Original draft"}`)
			var d store.Draft
			json.Unmarshal(r.Body.Bytes(), &d)
			if r.Code != 201 {
				t.Fatal(r.Body)
			}
			if publishing {
				if _, err := st.ApproveDraft(1, d.ID); err != nil {
					t.Fatal(err)
				}
			}
			p := &blockingReplyLLM{started: make(chan struct{}), release: make(chan struct{})}
			s.newLLM = func() (llm.Provider, error) { return p, nil }
			done := make(chan int, 1)
			go func() {
				done <- do(h, "POST", "/api/mentions/"+id+"/reply", `{"generate":true,"regenerate":true,"draft_id":`+strconv.FormatInt(d.ID, 10)+`}`).Code
			}()
			<-p.started
			if publishing {
				if _, err := st.BeginPublish(1, d.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				body := "Human edited during generation"
				if _, err := st.EditDraft(1, d.ID, store.DraftEdit{Body: &body}); err != nil {
					t.Fatal(err)
				}
			}
			close(p.release)
			if code := <-done; code != 409 {
				t.Fatalf("concurrent change status %d", code)
			}
			got, _ := st.Draft(1, d.ID)
			if publishing {
				if got.Status != store.DraftPublishing || got.Body != "Original draft" {
					t.Fatal("publish claim changed")
				}
			} else if got.Body != "Human edited during generation" {
				t.Fatal("human edit overwritten")
			}
		})
	}
}
