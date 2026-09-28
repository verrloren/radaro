// Package alerts delivers durable outbound alerts: batches of newly ingested
// negative mentions and volume/sentiment threshold episodes, over a generic
// webhook / Slack incoming webhook or SMTP email.
package alerts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

const userAgent = "radaro/0.1 (+https://github.com/verrloren/radaro)"

// Target is one configured delivery transport.
type Target interface {
	Name() string
	// Key is a stable, non-secret identifier used by the delivery outbox.
	Key() string
	SendMentions(ctx context.Context, query string, mentions []*model.Mention) error
	SendThreshold(ctx context.Context, text string, payload map[string]any) error
}

// Webhook posts JSON to a generic endpoint, or {"text": …} to Slack.
type Webhook struct {
	URL string
}

// NewWebhook validates the URL.
func NewWebhook(raw string) (*Webhook, error) {
	if _, err := validateWebhookURL(raw); err != nil {
		return nil, err
	}
	return &Webhook{URL: raw}, nil
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Key() string {
	sum := sha256.Sum256([]byte(w.URL))
	return hex.EncodeToString(sum[:])[:24]
}

func (w *Webhook) SendMentions(ctx context.Context, query string, mentions []*model.Mention) error {
	if len(mentions) == 0 {
		return nil
	}
	payload := map[string]any{
		"text":     AlertText(query, mentions),
		"event":    "radaro.negative_mentions",
		"query":    query,
		"count":    len(mentions),
		"mentions": mentionPayloads(mentions),
	}
	return w.post(ctx, payload)
}

func (w *Webhook) SendThreshold(ctx context.Context, text string, payload map[string]any) error {
	body := map[string]any{"text": text}
	for k, v := range payload {
		body[k] = v
	}
	return w.post(ctx, body)
}

func (w *Webhook) post(ctx context.Context, payload map[string]any) error {
	u, err := validateWebhookURL(w.URL)
	if err != nil {
		return err
	}
	if host := u.Hostname(); host == "hooks.slack.com" || host == "hooks.slack-gov.com" {
		payload = map[string]any{"text": payload["text"]}
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return errors.New("webhook request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Never echo the URL: Slack webhook URLs are credentials.
		return errors.New("webhook request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func validateWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("RADARO_WEBHOOK_URL must be an absolute http(s) URL")
	}
	return u, nil
}

// Email sends plain-text alerts over SMTP.
type Email struct {
	Host       string
	Port       int
	Sender     string
	Recipients []string
	Security   string // starttls | ssl | none
	Username   string
	Password   string
	Timeout    time.Duration
}

var emailRE = regexp.MustCompile(`^[^\s@<>,;:]+@[^\s@<>,;:]+$`)

// NewEmail validates and normalizes SMTP settings.
func NewEmail(e Email) (*Email, error) {
	e.Host = strings.TrimSpace(e.Host)
	e.Sender = strings.TrimSpace(e.Sender)
	e.Security = strings.ToLower(strings.TrimSpace(e.Security))
	if e.Security == "" {
		e.Security = "starttls"
	}
	if e.Timeout == 0 {
		e.Timeout = 15 * time.Second
	}
	var recipients []string
	seen := map[string]bool{}
	for _, r := range e.Recipients {
		r = strings.TrimSpace(r)
		if r != "" && !seen[r] {
			seen[r] = true
			recipients = append(recipients, r)
		}
	}
	e.Recipients = recipients
	switch {
	case e.Host == "" || strings.ContainsAny(e.Host, " \t\r\n"):
		return nil, errors.New("RADARO_SMTP_HOST must be a non-empty hostname")
	case e.Port < 1 || e.Port > 65535:
		return nil, errors.New("RADARO_SMTP_PORT must be between 1 and 65535")
	case e.Security != "starttls" && e.Security != "ssl" && e.Security != "none":
		return nil, errors.New("RADARO_SMTP_SECURITY must be starttls, ssl, or none")
	case len(e.Recipients) == 0:
		return nil, errors.New("RADARO_EMAIL_TO must contain at least one address")
	case !emailRE.MatchString(e.Sender):
		return nil, errors.New("RADARO_EMAIL_FROM contains an invalid email address")
	case (e.Username == "") != (e.Password == ""):
		return nil, errors.New("RADARO_SMTP_USERNAME and RADARO_SMTP_PASSWORD must be set together")
	}
	for _, r := range e.Recipients {
		if !emailRE.MatchString(r) {
			return nil, errors.New("RADARO_EMAIL_TO contains an invalid email address")
		}
	}
	return &e, nil
}

func (e *Email) Name() string { return "email" }

// Key identifies the delivery target without the SMTP password.
func (e *Email) Key() string {
	recipients := append([]string(nil), e.Recipients...)
	sort.Slice(recipients, func(i, j int) bool { return strings.ToLower(recipients[i]) < strings.ToLower(recipients[j]) })
	identity, _ := json.Marshal(map[string]any{
		"host": strings.ToLower(e.Host), "port": e.Port, "security": e.Security,
		"sender": e.Sender, "recipients": recipients, "username": e.Username,
	})
	sum := sha256.Sum256(identity)
	return "email-" + hex.EncodeToString(sum[:])[:24]
}

func (e *Email) SendMentions(ctx context.Context, query string, mentions []*model.Mention) error {
	if len(mentions) == 0 {
		return nil
	}
	subject := fmt.Sprintf("[Radaro] %d new negative mention%s: %s", len(mentions), plural(len(mentions)), safeHeader(query))
	return e.send(ctx, subject, AlertText(query, mentions))
}

func (e *Email) SendThreshold(ctx context.Context, text string, payload map[string]any) error {
	event := strings.TrimPrefix(fmt.Sprint(orDefault(payload["event"], "radaro.threshold_alert")), "radaro.")
	query := safeHeader(fmt.Sprint(orDefault(payload["query"], "tracked keyword")))
	details, _ := json.MarshalIndent(payload, "", "  ")
	subject := fmt.Sprintf("[Radaro] %s: %s", strings.ReplaceAll(event, "_", " "), query)
	return e.send(ctx, subject, text+"\n\nEvent details:\n"+string(details))
}

func (e *Email) send(ctx context.Context, subject, body string) error {
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	dialer := &net.Dialer{Timeout: e.Timeout}
	tlsConfig := &tls.Config{ServerName: e.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if e.Security == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return errors.New("email delivery failed: could not connect")
	}
	_ = conn.SetDeadline(time.Now().Add(e.Timeout * 2))
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return smtpError(err)
	}
	defer c.Close()
	if e.Security == "starttls" {
		if err := c.StartTLS(tlsConfig); err != nil {
			return smtpError(err)
		}
	}
	if e.Username != "" {
		if err := c.Auth(plainAuth{e.Username, e.Password, e.Host, e.Security != "none"}); err != nil {
			return smtpError(err)
		}
	}
	if err := c.Mail(e.Sender); err != nil {
		return smtpError(err)
	}
	for _, r := range e.Recipients {
		if err := c.Rcpt(r); err != nil {
			return smtpError(err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return smtpError(err)
	}
	msg := "From: " + e.Sender + "\r\n" +
		"To: " + strings.Join(e.Recipients, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return smtpError(err)
	}
	if err := w.Close(); err != nil {
		return smtpError(err)
	}
	return c.Quit()
}

// plainAuth is PLAIN auth that, unlike smtp.PlainAuth, also permits the
// explicitly configured "none" security mode for trusted local relays.
type plainAuth struct {
	username, password, host string
	requireTLS               bool
}

func (a plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if a.requireTLS && !server.TLS {
		return "", nil, errors.New("refusing to send credentials over an unencrypted connection")
	}
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected SMTP auth challenge")
	}
	return nil, nil
}

// smtpError keeps server reply codes but never credentials.
func smtpError(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		return fmt.Errorf("SMTP server returned %d", te.Code)
	}
	return errors.New("email delivery failed")
}

// AlertText renders a human-readable batch summary (first 10 mentions).
func AlertText(query string, mentions []*model.Mention) string {
	n := len(mentions)
	lines := []string{fmt.Sprintf("Radaro: %d new negative mention%s for “%s”", n, plural(n), query)}
	for i, m := range mentions {
		if i == 10 {
			break
		}
		excerpt := []rune(strings.Join(strings.Fields(m.Content()), " "))
		if len(excerpt) > 180 {
			excerpt = excerpt[:180]
		}
		src := m.Source
		if m.Author != nil && *m.Author != "" {
			src += " · " + *m.Author
		}
		line := "• " + src + ": " + string(excerpt)
		if m.URL != nil && *m.URL != "" {
			line += " — " + *m.URL
		}
		lines = append(lines, line)
	}
	if n > 10 {
		lines = append(lines, fmt.Sprintf("…and %d more", n-10))
	}
	return strings.Join(lines, "\n")
}

func mentionPayloads(mentions []*model.Mention) []map[string]any {
	out := make([]map[string]any, 0, len(mentions))
	for _, m := range mentions {
		text := []rune(m.Text)
		if len(text) > 500 {
			text = text[:500]
		}
		out = append(out, map[string]any{
			"id": m.ID, "source": m.Source, "author": m.Author, "title": m.Title,
			"text": string(text), "url": m.URL, "created_at": m.CreatedAt.Format(time.RFC3339),
			"score": m.Score, "sentiment_score": m.SentimentScore, "theme": m.Theme,
		})
	}
	return out
}

func safeHeader(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > 120 {
		v = string(r[:120])
	}
	if v == "" {
		return "tracked keyword"
	}
	return v
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func orDefault(v any, def string) any {
	if v == nil || v == "" {
		return def
	}
	return v
}
