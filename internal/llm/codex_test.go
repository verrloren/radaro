package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeCodex(t *testing.T, script string) *Codex {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"fixture"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return &Codex{Model: "fixture", Home: home, Path: path}
}

func TestCodexCompletionIsolation(t *testing.T) {
	t.Setenv("RADARO_TEST_PRIVATE_CREDENTIAL", "do-not-inherit")
	c := fakeCodex(t, `
test -z "$RADARO_TEST_PRIVATE_CREDENTIAL" || exit 5
test "$HOME" = "$PWD" || exit 6
test ! -e config.toml || exit 7
test "$(cat)" = 'untrusted post text' || exit 8
args="$*"
case "$args" in *'--disable shell_tool'*'--disable apps'*'--disable hooks'*'--disable multi_agent'*) ;; *) exit 9 ;; esac
printf '%s\n' '{"type":"item.completed","item":{"type":"reasoning","text":"private reasoning"}}' '{"type":"item.completed","item":{"type":"agent_message","text":"  Prepared comment  "}}' '{"type":"turn.completed"}'
`)
	got, err := c.Complete(context.Background(), "untrusted post text", "Write a helpful comment.", 1200)
	if err != nil || got != "Prepared comment" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestCodexRejectsFailedIncompleteAndToolRuns(t *testing.T) {
	for name, script := range map[string]string{
		"exit":       `echo 'access-token-fixture' >&2; exit 1`,
		"incomplete": `printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"partial"}}'`,
		"failed":     `printf '%s\n' '{"type":"turn.failed","error":{"message":"access-token-fixture"}}'`,
		"tool":       `printf '%s\n' '{"type":"item.started","item":{"type":"command_execution"}}'`,
		"bad-json":   `echo 'access-token-fixture'`,
	} {
		t.Run(name, func(t *testing.T) {
			body, err := fakeCodex(t, script).Complete(context.Background(), "test", "", 10)
			if err == nil || body != "" || strings.Contains(err.Error(), "access-token-fixture") {
				t.Fatalf("unsafe result %q %v", body, err)
			}
		})
	}
}

func TestCodexCancellationAndAvailability(t *testing.T) {
	c := fakeCodex(t, "exec sleep 10")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := c.Complete(ctx, "test", "", 10); err == nil || time.Since(started) > 2*time.Second {
		t.Fatal("cancellation did not stop completion")
	}
	if err := os.WriteFile(filepath.Join(c.Home, "auth.json"), []byte(`{"auth_mode":"api"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if c.Available() {
		t.Fatal("provider must require ChatGPT login")
	}
	if _, err := c.Complete(context.Background(), "test", "", 10); err == nil {
		t.Fatal("provider accepted missing login")
	}
}
