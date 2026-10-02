// Package config loads Radaro's runtime configuration from the environment
// (and an optional .env in the working directory). Everything is optional:
// with no configuration Radaro uses the zero-credential sources.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/verrloren/radaro/internal/llm"
	"github.com/verrloren/radaro/internal/sources"
)

// DataDir is where Radaro keeps its database and optional .env, so every
// working directory (and every agent) shares one state:
// $RADARO_HOME, else $XDG_DATA_HOME/radaro or ~/.local/share/radaro on Linux,
// ~/Library/Application Support/radaro on macOS, %LocalAppData%\radaro on Windows.
func DataDir() string {
	if dir := strings.TrimSpace(os.Getenv("RADARO_HOME")); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "radaro")
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return filepath.Join(dir, "radaro")
		}
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "radaro")
	}
	return filepath.Join(home, ".local", "share", "radaro")
}

// Config is the full runtime configuration.
type Config struct {
	DBPath         string
	Sources        []string
	PerSourceLimit int
	SourceRetries  int
	RetryBackoff   float64 // seconds

	SentimentAnalyzer string // lexicon | llm
	LLM               llm.Settings

	SourceOptions sources.Options

	WebhookURL   string
	EmailTo      []string
	EmailFrom    string
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string // starttls | ssl | none
	SMTPUsername string
	SMTPPassword string

	AlertWindowHours      int
	AlertBaselineWindows  int
	AlertMinMentions      int
	AlertVolumeMultiplier float64
	AlertSentimentDrop    float64
	AlertCooldownHours    int

	JWTSecret    string // blank: a random secret kept in the database
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	Registration string // open | closed (the first user can always sign up)
	CookieSecure bool   // force Secure cookies behind a proxy that hides HTTPS

	// AccountCheckInterval is how often serve checks publishing accounts and
	// refreshes published drafts' metrics; 0 disables it.
	AccountCheckInterval time.Duration
}

// Load reads .env (without overriding the real environment) and validates.
func Load() (*Config, error) {
	// A .env in the working directory wins, then the one in the data dir;
	// neither overrides the real environment. Missing files are fine.
	_ = godotenv.Load()
	_ = godotenv.Load(filepath.Join(DataDir(), ".env"))
	var errs []error
	e := &envReader{errs: &errs}

	c := &Config{
		DBPath:            orDefault(e.str("RADARO_DB"), filepath.Join(DataDir(), "radaro.db")),
		Sources:           e.list("RADARO_SOURCES"),
		PerSourceLimit:    e.int("RADARO_LIMIT", 50, 1),
		SourceRetries:     e.int("RADARO_RETRIES", 2, 0),
		RetryBackoff:      e.float("RADARO_RETRY_BACKOFF", 1.0, 0.001),
		SentimentAnalyzer: e.choice("RADARO_SENTIMENT_ANALYZER", "lexicon", "lexicon", "llm"),
		LLM: llm.Settings{
			Provider:        orDefault(e.str("RADARO_LLM_PROVIDER"), "none"),
			Model:           e.str("RADARO_LLM_MODEL"),
			AnthropicAPIKey: e.str("ANTHROPIC_API_KEY"),
			OpenAIAPIKey:    orDefault(e.str("RADARO_LLM_API_KEY"), e.str("OPENAI_API_KEY")),
			OpenAIBaseURL:   e.str("RADARO_LLM_BASE_URL"),
			OllamaHost:      e.str("RADARO_LLM_HOST"),
			CodexHome:       e.str("RADARO_CODEX_HOME"),
			CodexPath:       e.str("RADARO_CODEX_PATH"),
			ReasoningEffort: e.choice("RADARO_LLM_REASONING_EFFORT", "medium", "low", "medium", "high", "xhigh", "max", "ultra"),
		},
		SourceOptions: sources.Options{
			RedditClientID:      e.str("RADARO_REDDIT_CLIENT_ID"),
			RedditClientSecret:  e.str("RADARO_REDDIT_CLIENT_SECRET"),
			RedditAccessToken:   e.str("RADARO_REDDIT_ACCESS_TOKEN"),
			MastodonInstance:    e.str("RADARO_MASTODON_INSTANCE"), // blank = mastodon.social
			MastodonAccessToken: e.str("RADARO_MASTODON_ACCESS_TOKEN"),
			RSSFeeds:            e.list("RADARO_RSS_FEEDS"),
			XBearerToken:        e.str("RADARO_X_BEARER_TOKEN"),
			YouTubeAPIKey:       e.str("RADARO_YOUTUBE_API_KEY"),
		},
		WebhookURL:            e.str("RADARO_WEBHOOK_URL"),
		EmailTo:               e.list("RADARO_EMAIL_TO"),
		EmailFrom:             e.str("RADARO_EMAIL_FROM"),
		SMTPHost:              e.str("RADARO_SMTP_HOST"),
		SMTPPort:              e.int("RADARO_SMTP_PORT", 587, 1),
		SMTPSecurity:          e.choice("RADARO_SMTP_SECURITY", "starttls", "starttls", "ssl", "none"),
		SMTPUsername:          e.str("RADARO_SMTP_USERNAME"),
		SMTPPassword:          e.str("RADARO_SMTP_PASSWORD"),
		AlertWindowHours:      e.int("RADARO_ALERT_WINDOW_HOURS", 24, 1),
		AlertBaselineWindows:  e.int("RADARO_ALERT_BASELINE_WINDOWS", 7, 1),
		AlertMinMentions:      e.int("RADARO_ALERT_MIN_MENTIONS", 5, 1),
		AlertVolumeMultiplier: e.float("RADARO_ALERT_VOLUME_MULTIPLIER", 0, 0),
		AlertSentimentDrop:    e.float("RADARO_ALERT_SENTIMENT_DROP", 0, 0),
		AlertCooldownHours:    e.int("RADARO_ALERT_COOLDOWN_HOURS", 24, 0),
		JWTSecret:             e.str("RADARO_JWT_SECRET"),
		AccessTTL:             e.duration("RADARO_ACCESS_TTL", 15*time.Minute, time.Minute),
		RefreshTTL:            e.duration("RADARO_REFRESH_TTL", 30*24*time.Hour, time.Hour),
		Registration:          e.choice("RADARO_REGISTRATION", "closed", "open", "closed"),
		CookieSecure:          e.choice("RADARO_COOKIE_SECURE", "false", "true", "false") == "true",
		AccountCheckInterval:  e.optionalDuration("RADARO_ACCOUNT_CHECK_INTERVAL", 6*time.Hour, time.Minute),
	}
	if len(c.Sources) == 0 {
		c.Sources = append([]string(nil), sources.DefaultSources...)
	}
	for i, s := range c.Sources {
		c.Sources[i] = strings.ToLower(s)
	}
	if c.SMTPPort > 65535 {
		errs = append(errs, fmt.Errorf("RADARO_SMTP_PORT must be at most 65535 (got %d)", c.SMTPPort))
	}
	emailSet := len(c.EmailTo) > 0 || c.EmailFrom != "" || c.SMTPHost != "" || c.SMTPUsername != "" || c.SMTPPassword != ""
	if emailSet && (len(c.EmailTo) == 0 || c.EmailFrom == "" || c.SMTPHost == "") {
		errs = append(errs, errors.New("RADARO_EMAIL_TO, RADARO_EMAIL_FROM and RADARO_SMTP_HOST must be set together"))
	}
	if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
		errs = append(errs, errors.New("RADARO_SMTP_USERNAME and RADARO_SMTP_PASSWORD must be set together"))
	}
	if c.JWTSecret != "" && len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("RADARO_JWT_SECRET must be at least 32 bytes (openssl rand -base64 48)"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

type envReader struct{ errs *[]error }

// str returns the trimmed value; a set-but-blank variable counts as unset.
func (e *envReader) str(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func (e *envReader) list(name string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(name), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (e *envReader) int(name string, def, minimum int) int {
	raw := e.str(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be an integer (got %q)", name, raw))
		return def
	}
	if v < minimum {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be at least %d (got %d)", name, minimum, v))
	}
	return v
}

func (e *envReader) float(name string, def, minimum float64) float64 {
	raw := e.str(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be a number (got %q)", name, raw))
		return def
	}
	if v < minimum {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be at least %g (got %g)", name, minimum, v))
	}
	return v
}

func (e *envReader) duration(name string, def, minimum time.Duration) time.Duration {
	raw := e.str(name)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be a duration like 15m or 720h (got %q)", name, raw))
		return def
	}
	if v < minimum {
		*e.errs = append(*e.errs, fmt.Errorf("%s must be at least %s (got %s)", name, minimum, v))
	}
	return v
}

// optionalDuration is duration for something that can be switched off: a
// zero duration ("0", "0s") disables it, and anything else must reach minimum.
func (e *envReader) optionalDuration(name string, def, minimum time.Duration) time.Duration {
	if v, err := time.ParseDuration(e.str(name)); err == nil && v == 0 {
		return 0
	}
	return e.duration(name, def, minimum)
}

func (e *envReader) choice(name, def string, choices ...string) string {
	v := strings.ToLower(orDefault(e.str(name), def))
	for _, c := range choices {
		if v == c {
			return v
		}
	}
	*e.errs = append(*e.errs, fmt.Errorf("%s must be one of %s (got %q)", name, strings.Join(choices, ", "), v))
	return def
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
