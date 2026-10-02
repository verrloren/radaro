package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/verrloren/radaro/internal/draftprep"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/redditbrowser"
	"github.com/verrloren/radaro/internal/store"
)

var errRedditBrowserRequired = errors.New("connect a Reddit browser account to load post details")

type replyRequests struct {
	sync.Mutex
	active map[string]bool
}

func (s *Server) replyMention(w http.ResponseWriter, r *http.Request) (*model.Mention, int64, bool) {
	pid, ok := optionalID(w, r.URL.Query().Get("project_id"))
	if !ok {
		return nil, 0, false
	}
	if pid != 0 && !s.ownProject(w, r, pid) {
		return nil, 0, false
	}
	ms, err := s.store.Mentions(store.MentionFilter{Scope: store.Scope{UserID: userID(r), ProjectID: pid}, ID: chi.URLParam(r, "mention"), Limit: 1})
	if err != nil {
		internalError(w, err)
		return nil, 0, false
	}
	if len(ms) == 0 {
		writeError(w, 404, "mention not found")
		return nil, 0, false
	}
	m := ms[0]
	if m.Source != "reddit" || m.URL == nil {
		writeError(w, 422, "a Reddit post is required")
		return nil, 0, false
	}
	if pid == 0 {
		pid, err = s.store.ProjectForQuery(userID(r), m.Query)
		if err != nil {
			storeError(w, err, 500)
			return nil, 0, false
		}
	}
	return m, pid, true
}

func (s *Server) readPost(ctx context.Context, uid, pid int64, target string) (redditbrowser.PostDetails, error) {
	opts, err := pipeline.SourceOptions(s.cfg, s.store, uid, pid)
	if err != nil {
		return redditbrowser.PostDetails{}, err
	}
	if opts.RedditBrowser == nil {
		return redditbrowser.PostDetails{}, errRedditBrowserRequired
	}
	return s.loadRedditPost(ctx, *opts.RedditBrowser, target)
}

func (s *Server) redditPostDetails(w http.ResponseWriter, r *http.Request) {
	m, pid, ok := s.replyMention(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	post, err := s.readPost(ctx, userID(r), pid, *m.URL)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	d, err := s.store.ReplyDraft(userID(r), pid, m.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	var draft any
	if d != nil {
		draft = s.detail(userID(r), d)
	}
	writeJSON(w, 200, map[string]any{"post": post, "project_id": pid, "draft": draft})
}

func (s *Server) prepareReply(w http.ResponseWriter, r *http.Request) {
	m, pid, ok := s.replyMention(w, r)
	if !ok {
		return
	}
	uid := userID(r)
	key := strings.Join([]string{strconv.FormatInt(uid, 10), strconv.FormatInt(pid, 10), m.ID}, ":")
	s.replyRequests.Lock()
	if s.replyRequests.active == nil {
		s.replyRequests.active = map[string]bool{}
	}
	if s.replyRequests.active[key] {
		s.replyRequests.Unlock()
		writeError(w, 409, "reply preparation is already running")
		return
	}
	s.replyRequests.active[key] = true
	s.replyRequests.Unlock()
	defer func() { s.replyRequests.Lock(); delete(s.replyRequests.active, key); s.replyRequests.Unlock() }()
	d, err := s.store.ReplyDraft(uid, pid, m.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	var body struct {
		Body       string `json:"body"`
		Generate   bool   `json:"generate"`
		Regenerate bool   `json:"regenerate"`
		DraftID    int64  `json:"draft_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Regenerate && (!body.Generate || d == nil || body.DraftID != d.ID) {
		writeError(w, 409, "the reply draft has changed; reload before regenerating")
		return
	}
	if d != nil && !body.Regenerate {
		writeJSON(w, 200, s.detail(uid, d))
		return
	}
	if d != nil && (d.Status == store.DraftPublished || d.Status == store.DraftPublishing) {
		writeError(w, 409, "published or publishing replies cannot be regenerated")
		return
	}
	if body.Generate {
		provider, err := s.newLLM()
		if err != nil || !provider.Available() {
			writeError(w, 409, "LLM is not configured; write a reply manually")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		target := *m.URL
		if d != nil && d.ReplyTo != nil {
			target = *d.ReplyTo
		}
		post, err := s.readPost(ctx, uid, pid, target)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
		if !post.CanReply || post.Removed {
			writeError(w, 422, "this Reddit post is not open for replies")
			return
		}
		settings, err := s.store.ReplySettings(uid, pid)
		if err != nil {
			internalError(w, err)
			return
		}
		body.Body, err = draftprep.Reddit(ctx, provider, settings, post)
		s.recordLLM(err == nil)
		if err != nil {
			writeError(w, 502, err.Error())
			return
		}
	}
	body.Body = strings.TrimSpace(body.Body)
	n := store.NewDraft{UserID: uid, ProjectID: pid, Platform: "reddit", Kind: "reply", Body: body.Body, ReplyTo: *m.URL, Query: m.Query, MentionID: m.ID}
	preview := store.Draft{Platform: n.Platform, Kind: n.Kind, Body: n.Body, ReplyTo: &n.ReplyTo}
	if err := ValidateDraft(&preview); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if d != nil {
		d, err = s.store.EditDraft(uid, d.ID, store.DraftEdit{Body: &body.Body, ExpectedUpdatedAt: &d.UpdatedAt})
		if err != nil {
			storeError(w, err, 422)
			return
		}
		_ = s.store.LogActivity(uid, "draft.edited", d.ID, DraftSummary(d))
		writeJSON(w, 200, s.detail(uid, d))
		return
	}
	d, err = s.store.CreateDraft(n)
	if err != nil {
		storeError(w, err, 422)
		return
	}
	_ = s.store.LogActivity(uid, "draft.created", d.ID, DraftSummary(d))
	writeJSON(w, 201, s.detail(uid, d))
}

func (s *Server) getReplySettings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	settings, err := s.store.ReplySettings(userID(r), id)
	if err != nil {
		storeError(w, err, 500)
		return
	}
	writeJSON(w, 200, settings)
}

func (s *Server) saveReplySettings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.ownProject(w, r, id) {
		return
	}
	var settings store.ReplySettings
	if !decode(w, r, &settings) {
		return
	}
	settings.Brief = strings.TrimSpace(settings.Brief)
	settings.Instructions = strings.TrimSpace(settings.Instructions)
	settings.Language = strings.TrimSpace(settings.Language)
	settings.Tone = strings.TrimSpace(settings.Tone)
	if len(settings.Brief) > 8000 || len(settings.Instructions) > 8000 || len(settings.Language) > 100 || len(settings.Tone) > 500 {
		writeError(w, 422, "reply settings are too long")
		return
	}
	if err := s.store.SaveReplySettings(userID(r), id, settings); err != nil {
		storeError(w, err, 500)
		return
	}
	writeJSON(w, 200, settings)
}
