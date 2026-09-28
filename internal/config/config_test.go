package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // no stray .env
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != "radaro.db" || strings.Join(c.Sources, ",") != "hackernews,bluesky" || c.PerSourceLimit != 50 {
		t.Fatalf("defaults %+v", c)
	}
	if c.SourceOptions.MastodonInstance != "mastodon.social" || c.SMTPSecurity != "starttls" {
		t.Fatalf("defaults %+v", c)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	t.Chdir(t.TempDir())
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
