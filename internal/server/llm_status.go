package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/verrloren/radaro/internal/llm"
)

type llmStatus struct {
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	Configured bool    `json:"configured"`
	Status     string  `json:"status"`
	CheckedAt  *string `json:"checked_at"`
	Detail     string  `json:"detail"`
}

type llmConnection struct {
	sync.Mutex
	checkedAt      *string
	status, detail string
}

func (s *Server) llmView() llmStatus {
	p, err := s.newLLM()
	v := llmStatus{Provider: s.cfg.LLM.Provider, Model: s.cfg.LLM.Model, Status: "not_configured", Detail: "LLM is not configured. Replies can be written manually."}
	if v.Provider == "" {
		v.Provider = "none"
	}
	if err != nil {
		return v
	}
	v.Configured = p.Available()
	switch p := p.(type) {
	case *llm.OpenAI:
		v.Model = p.Model
	case *llm.Anthropic:
		v.Model = p.Model
	case *llm.Ollama:
		v.Model = p.Model
	}
	if !v.Configured {
		return v
	}
	v.Status = "unchecked"
	v.Detail = "Configured; connection has not been checked yet."
	s.llmConnection.Lock()
	defer s.llmConnection.Unlock()
	if s.llmConnection.checkedAt != nil {
		v.CheckedAt = s.llmConnection.checkedAt
		v.Status = s.llmConnection.status
		v.Detail = s.llmConnection.detail
	}
	return v
}

func (s *Server) getLLMStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.llmView())
}

func (s *Server) checkLLM(w http.ResponseWriter, r *http.Request) {
	p, err := s.newLLM()
	if err != nil || !p.Available() {
		writeJSON(w, 200, s.llmView())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	body, err := p.Complete(ctx, "Reply with OK.", "This is a connection check.", 8)
	s.recordLLM(err == nil && strings.TrimSpace(body) != "")
	writeJSON(w, 200, s.llmView())
}

func (s *Server) recordLLM(ok bool) {
	now := time.Now().UTC().Format(time.RFC3339)
	s.llmConnection.Lock()
	defer s.llmConnection.Unlock()
	s.llmConnection.checkedAt = &now
	s.llmConnection.status = "connected"
	s.llmConnection.detail = "Last request succeeded."
	if !ok {
		s.llmConnection.status = "error"
		s.llmConnection.detail = "LLM request failed. Check provider, credentials, model and network."
	}
}
