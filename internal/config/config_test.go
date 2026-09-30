package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Variables the tests set more than once.
const (
	envHome  = "RADARO_HOME"
	envLimit = "RADARO_LIMIT"
)

func TestDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // no stray .env
	home := t.TempDir()
	t.Setenv(envHome, home)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != filepath.Join(home, "radaro.db") || strings.Join(c.Sources, ",") != "hackernews,bluesky" || c.PerSourceLimit != 50 {
		t.Fatalf("defaults %+v", c)
	}
	if c.SourceOptions.MastodonInstance != "" || c.SMTPSecurity != "starttls" || c.AccountCheckInterval != 6*time.Hour {
		t.Fatalf("defaults %+v", c)
	}
}

func TestAccountCheckInterval(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(envHome, t.TempDir())
	const name = "RADARO_ACCOUNT_CHECK_INTERVAL"
	for raw, want := range map[string]time.Duration{"0": 0, "0s": 0, "90m": 90 * time.Minute, "1m": time.Minute} {
		t.Setenv(name, raw)
		c, err := Load()
		if err != nil || c.AccountCheckInterval != want {
			t.Fatalf("%s: %+v %v", raw, c, err)
		}
	}
	for _, raw := range []string{"often", "10s", "6", "-1h"} {
		t.Setenv(name, raw)
		if _, err := Load(); err == nil {
			t.Fatalf("%s should fail", raw)
		}
	}
	// The session lifetimes still refuse 0.
	t.Setenv(name, "")
	t.Setenv("RADARO_ACCESS_TTL", "0")
	if _, err := Load(); err == nil {
		t.Fatal("RADARO_ACCESS_TTL=0 should fail")
	}
}

func TestDataDirEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv(envHome, home)
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("RADARO_LIMIT=7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv(envLimit) }) // godotenv sets it process-wide
	c, err := Load()
	if err != nil || c.PerSourceLimit != 7 {
		t.Fatalf("data-dir .env not loaded: %+v %v", c, err)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(envHome, t.TempDir())
	t.Setenv("RADARO_SOURCES", " HackerNews , reddit ,")
	t.Setenv(envLimit, "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Sources, ",") != "hackernews,reddit" || c.PerSourceLimit != 50 {
		t.Fatalf("got %+v", c)
	}

	for name, env := range map[string][2]string{
		"bad int":      {envLimit, "lots"},
		"below min":    {envLimit, "0"},
		"bad choice":   {"RADARO_SMTP_SECURITY", "tls9"},
		"partial smtp": {"RADARO_EMAIL_TO", "a@example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s should fail", env[0], env[1])
			}
		})
	}
}
