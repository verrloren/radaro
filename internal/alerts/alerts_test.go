package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/store"
)

func TestWebhookPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
	}))
	defer srv.Close()
	w, err := NewWebhook(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := &model.Mention{Source: "reddit", Query: "go", Author: model.Str("u"), Text: "awful", URL: model.Str("https://x"), CreatedAt: time.Now()}
	m.Normalize()
	if err := w.SendMentions(context.Background(), "go", []*model.Mention{m}); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "radaro.negative_mentions" || !strings.Contains(got["text"].(string), "reddit · u: awful — https://x") {
		t.Fatalf("payload %v", got)
	}
}

func TestWebhookErrorHidesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	w, _ := NewWebhook(srv.URL + "/secret-token")
	err := w.SendThreshold(context.Background(), "t", map[string]any{})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error %v", err)
	}
	if _, err := NewWebhook("ftp://x"); err == nil {
		t.Fatal("non-http URL should be rejected")
	}
}

func TestEmailValidationAndKey(t *testing.T) {
	base := Email{Host: "smtp.example.com", Port: 587, Sender: "a@example.com", Recipients: []string{"b@example.com"}}
	e, err := NewEmail(base)
	if err != nil {
		t.Fatal(err)
	}
	withPassword := base
	withPassword.Username, withPassword.Password = "u", "p1"
	e1, _ := NewEmail(withPassword)
	withPassword.Password = "p2"
	e2, _ := NewEmail(withPassword)
	if e1.Key() != e2.Key() || e1.Key() == e.Key() {
		t.Fatal("key must ignore the password but include the username")
	}
	bad := base
	bad.Sender = "not-an-email"
	if _, err := NewEmail(bad); err == nil {
		t.Fatal("invalid sender accepted")
	}
	bad = base
	bad.Username = "only-user"
	if _, err := NewEmail(bad); err == nil {
		t.Fatal("username without password accepted")
	}
}

func TestEvaluateThresholds(t *testing.T) {
	neg, pos := -0.5, 0.5
	s := ThresholdSettings{WindowHours: 24, MinimumMentions: 5, VolumeMultiplier: 2, SentimentDrop: 0.25}
	ev := EvaluateThresholds("go", store.AlertMetrics{
		CurrentCount: 10, BaselineCount: 14, BaselineAverage: 2,
		CurrentNetSentiment: &neg, BaselineNetSentiment: &pos,
	}, s)
	if ev["volume_spike"] == nil || ev["sentiment_drop"] == nil {
		t.Fatalf("expected both events: %+v", ev)
	}
	if !strings.Contains(ev["volume_spike"].Text, "5.0× the baseline") {
		t.Fatalf("text %q", ev["volume_spike"].Text)
	}
	quiet := EvaluateThresholds("go", store.AlertMetrics{CurrentCount: 3, BaselineCount: 14, BaselineAverage: 2}, s)
	if quiet["volume_spike"] != nil || quiet["sentiment_drop"] != nil {
		t.Fatal("below minimum mentions must not alert")
	}
	off := EvaluateThresholds("go", store.AlertMetrics{CurrentCount: 10, BaselineCount: 14, BaselineAverage: 2}, ThresholdSettings{MinimumMentions: 5})
	if off["volume_spike"] != nil {
		t.Fatal("zero multiplier disables the check")
	}
}
