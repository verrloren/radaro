// Package llm holds the optional, swappable LLM providers. Radaro works fully
// without any of them: sentiment falls back to the lexicon and themes to
// term-frequency clustering.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider turns a (system, prompt) pair into text.
type Provider interface {
	Name() string
	Available() bool
	Complete(ctx context.Context, prompt, system string, maxTokens int) (string, error)
}

// Settings configure provider construction.
type Settings struct {
	Provider        string // none | anthropic | openai | ollama | codex
	Model           string
	AnthropicAPIKey string
	OpenAIAPIKey    string
	OpenAIBaseURL   string
	OllamaHost      string
	CodexHome       string
	CodexPath       string
}

// New returns the configured provider.
func New(s Settings) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(s.Provider)) {
	case "", "none", "null":
		return None{}, nil
	case "anthropic":
		return &Anthropic{Model: orDefault(s.Model, "claude-sonnet-5"), APIKey: s.AnthropicAPIKey}, nil
	case "openai":
		return &OpenAI{
			Model:   orDefault(s.Model, "gpt-4o-mini"),
			APIKey:  s.OpenAIAPIKey,
			BaseURL: strings.TrimRight(orDefault(s.OpenAIBaseURL, "https://api.openai.com/v1"), "/"),
		}, nil
	case "ollama":
		return &Ollama{
			Model: orDefault(s.Model, "llama3.2"),
			Host:  strings.TrimRight(orDefault(s.OllamaHost, "http://localhost:11434"), "/"),
		}, nil
	case "codex":
		return newCodex(s), nil
	}
	return nil, fmt.Errorf("unknown LLM provider %q (options: none, anthropic, openai, ollama, codex)", s.Provider)
}

// Enabled reports whether a named (non-null) provider is selected.
func Enabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none", "null":
		return false
	}
	return true
}

// None is the default provider: it never calls out.
type None struct{}

func (None) Name() string    { return "none" }
func (None) Available() bool { return false }
func (None) Complete(context.Context, string, string, int) (string, error) {
	return "", fmt.Errorf("no LLM provider configured; set RADARO_LLM_PROVIDER (anthropic|openai|ollama|codex)")
}

// Anthropic calls the Claude Messages API.
type Anthropic struct {
	Model  string
	APIKey string
}

func (a *Anthropic) Name() string    { return "anthropic" }
func (a *Anthropic) Available() bool { return a.APIKey != "" }

func (a *Anthropic) Complete(ctx context.Context, prompt, system string, maxTokens int) (string, error) {
	body := map[string]any{
		"model":      a.Model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	if system != "" {
		body["system"] = system
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	headers := map[string]string{"x-api-key": a.APIKey, "anthropic-version": "2023-06-01"}
	if err := postJSON(ctx, "https://api.anthropic.com/v1/messages", headers, body, &out, 60*time.Second); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// OpenAI calls any OpenAI-compatible /chat/completions endpoint.
type OpenAI struct {
	Model   string
	APIKey  string
	BaseURL string
}

func (o *OpenAI) Name() string    { return "openai" }
func (o *OpenAI) Available() bool { return o.APIKey != "" }

func (o *OpenAI) Complete(ctx context.Context, prompt, system string, maxTokens int) (string, error) {
	var messages []map[string]string
	if system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	messages = append(messages, map[string]string{"role": "user", "content": prompt})
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	body := map[string]any{"model": o.Model, "messages": messages, "max_tokens": maxTokens}
	if err := postJSON(ctx, o.BaseURL+"/chat/completions", map[string]string{"Authorization": "Bearer " + o.APIKey}, body, &out, 60*time.Second); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai response had no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// Ollama calls a local Ollama server, keeping analysis on the machine.
type Ollama struct {
	Model string
	Host  string
}

func (o *Ollama) Name() string    { return "ollama" }
func (o *Ollama) Available() bool { return true } // errors surface on call if the server is down

func (o *Ollama) Complete(ctx context.Context, prompt, system string, maxTokens int) (string, error) {
	var out struct {
		Response string `json:"response"`
	}
	body := map[string]any{
		"model":   o.Model,
		"prompt":  prompt,
		"system":  system,
		"stream":  false,
		"options": map[string]any{"num_predict": maxTokens},
	}
	if err := postJSON(ctx, o.Host+"/api/generate", nil, body, &out, 120*time.Second); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Response), nil
}

func postJSON(ctx context.Context, url string, headers map[string]string, body, out any, timeout time.Duration) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ParseJSONObject extracts a JSON object from a model reply, tolerating a
// surrounding ``` fence. It returns nil when the reply is not an object.
func ParseJSONObject(raw string) map[string]json.RawMessage {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		parts := strings.SplitN(raw, "```", 3)
		if len(parts) >= 2 {
			raw = strings.TrimSpace(parts[1])
			if strings.HasPrefix(strings.ToLower(raw), "json") {
				raw = strings.TrimSpace(raw[4:])
			}
		}
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
