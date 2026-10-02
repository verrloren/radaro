package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Codex uses the CLI's ChatGPT login and renewal without exposing tokens to Radaro.
// Calls share a gate so concurrent requests cannot race auth-cache renewal.
type Codex struct {
	Model, Home, Path string
}

var codexGate = make(chan struct{}, 1)

func newCodex(s Settings) *Codex {
	home, _ := os.UserHomeDir()
	return &Codex{Model: orDefault(s.Model, "gpt-6.1-sol"), Home: orDefault(s.CodexHome, filepath.Join(home, ".codex")), Path: orDefault(s.CodexPath, "codex")}
}

func (*Codex) Name() string { return "codex" }

func (c *Codex) Available() bool {
	if _, err := exec.LookPath(c.Path); err != nil {
		return false
	}
	f, err := os.Open(filepath.Join(c.Home, "auth.json"))
	if err != nil {
		return false
	}
	defer f.Close()
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	return json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&auth) == nil && auth.Mode == "chatgpt" && auth.Tokens.AccessToken != ""
}

func (c *Codex) Complete(ctx context.Context, prompt, system string, maxTokens int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case codexGate <- struct{}{}:
		defer func() { <-codexGate }()
	case <-ctx.Done():
		return "", errors.New("Codex request timed out or was canceled")
	}
	if !c.Available() {
		return "", errors.New("Codex CLI or ChatGPT login is unavailable")
	}
	work, err := os.MkdirTemp("", "radaro-completion-")
	if err != nil {
		return "", errors.New("Codex workspace could not be created")
	}
	defer os.RemoveAll(work)
	args := codexArgs(c.Model, system, maxTokens)
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "CODEX_HOME=" + c.Home, "TMPDIR=" + work}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = io.Discard // CLI errors may contain provider diagnostics and credentials.
	cmd.WaitDelay = time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return "", errors.New("Codex could not start")
	}
	var body string
	var completed, invalid bool
	scan := bufio.NewScanner(io.LimitReader(pipe, 1<<20))
	scan.Buffer(make([]byte, 4096), 128<<10)
	for scan.Scan() {
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil {
			invalid = true
			cancel()
			break
		}
		if event.Type == "turn.failed" || event.Type == "error" {
			invalid = true
		}
		if event.Type == "item.started" || event.Type == "item.completed" || event.Type == "item.updated" {
			switch event.Item.Type {
			case "agent_message", "reasoning", "error":
			default:
				// Fail closed if a future CLI version unexpectedly exposes tools.
				invalid = true
				cancel()
			}
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			body = event.Item.Text
		}
		if event.Type == "turn.completed" {
			completed = true
		}
	}
	if scan.Err() != nil {
		cancel()
	}
	err = cmd.Wait()
	if err != nil || scan.Err() != nil || invalid || !completed || strings.TrimSpace(body) == "" || len(body) > 40000 {
		return "", errors.New("Codex completion failed; check login, model, limits and network")
	}
	return strings.TrimSpace(body), nil
}

func codexArgs(model, system string, maxTokens int) []string {
	instructions := "You are a text completion service. Return only the requested text. Do not use tools, read files, browse, or perform actions. " + system
	if maxTokens > 0 {
		instructions += fmt.Sprintf(" Keep the answer within %d tokens.", maxTokens)
	}
	args := []string{"exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--json", "--color", "never", "--model", model,
		"-c", "approval_policy=\"never\"", "-c", "cli_auth_credentials_store=\"file\"", "-c", "web_search=\"disabled\"", "-c", "project_doc_max_bytes=0", "-c", "model_reasoning_effort=\"low\"", "-c", "analytics.enabled=false", "-c", "feedback.enabled=false", "-c", "developer_instructions=" + strconv.Quote(instructions)}
	for _, feature := range []string{"shell_tool", "apps", "plugins", "remote_plugin", "hooks", "multi_agent", "goals", "memories", "shell_snapshot", "browser_use", "computer_use", "image_generation", "sleep_tool", "skill_search", "workspace_dependencies", "in_app_browser", "code_mode", "code_mode_host", "view_image"} {
		args = append(args, "--disable", feature)
	}
	return append(args, "-")
}
