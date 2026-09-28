// Package config loads Radaro's runtime configuration from the environment
// (and an optional .env in the working directory). Everything is optional:
// with no configuration Radaro uses the zero-credential sources.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/verrloren/radaro/internal/llm"
	"github.com/verrloren/radaro/internal/sources"
)

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
}

// Load reads .env (without overriding the real environment) and validates.
func Load() (*Config, error) {
	_ = godotenv.Load() // a missing .env is fine
	var errs []error
	e := &envReader{errs: &errs}

	c := &Config{
		DBPath:            orDefault(e.str("RADARO_DB"), "radaro.db"),
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
		},
		SourceOptions: sources.Options{
			RedditClientID:      e.str("RADARO_REDDIT_CLIENT_ID"),
			RedditClientSecret:  e.str("RADARO_REDDIT_CLIENT_SECRET"),
			RedditAccessToken:   e.str("RADARO_REDDIT_ACCESS_TOKEN"),
			MastodonInstance:    orDefault(e.str("RADARO_MASTODON_INSTANCE"), "mastodon.social"),
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
