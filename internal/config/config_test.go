package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // no stray .env
	home := t.TempDir()
	t.Setenv("RADARO_HOME", home)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != filepath.Join(home, "radaro.db") || strings.Join(c.Sources, ",") != "hackernews,bluesky" || c.PerSourceLimit != 50 {
		t.Fatalf("defaults %+v", c)
	}
	if c.SourceOptions.MastodonInstance != "" || c.SMTPSecurity != "starttls" {
		t.Fatalf("defaults %+v", c)
	}
}

func TestDataDirEnvFile(t *testing.T) {
	t.Chdir(t.TempDir())
	home := t.TempDir()
	t.Setenv("RADARO_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("RADARO_LIMIT=7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv("RADARO_LIMIT") }) // godotenv sets it process-wide
	c, err := Load()
	if err != nil || c.PerSourceLimit != 7 {
		t.Fatalf("data-dir .env not loaded: %+v %v", c, err)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RADARO_HOME", t.TempDir())
	t.Setenv("RADARO_SOURCES", " HackerNews , reddit ,")
	t.Setenv("RADARO_LIMIT", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Sources, ",") != "hackernews,reddit" || c.PerSourceLimit != 50 {
		t.Fatalf("got %+v", c)
	}

	for name, env := range map[string][2]string{
		"bad int":      {"RADARO_LIMIT", "lots"},
		"below min":    {"RADARO_LIMIT", "0"},
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
