// Package model defines the Mention type every part of Radaro works with.
//
// Sources produce mentions, analyzers annotate them, the store persists them,
// and the dashboard renders them.
package model

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
)

// Sentiment is a coarse label. Kept deliberately small — useful, not precise.
type Sentiment string

const (
	Positive Sentiment = "positive"
	Neutral  Sentiment = "neutral"
	Negative Sentiment = "negative"
)

// ParseSentiment returns the label for s, or false when it is not one of the three.
func ParseSentiment(s string) (Sentiment, bool) {
	switch Sentiment(strings.ToLower(strings.TrimSpace(s))) {
	case Positive:
		return Positive, true
	case Neutral:
		return Neutral, true
	case Negative:
		return Negative, true
	}
	return "", false
}

// Mention is one thing someone said, somewhere, that matched a tracked query.
//
// ID is a stable content hash so the same item fetched twice (or by two
// overlapping queries) de-duplicates cleanly in the store.
type Mention struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Query          string    `json:"query"`
	Author         *string   `json:"author"`
	Title          *string   `json:"title"`
	Text           string    `json:"text"`
	URL            *string   `json:"url"`
	CreatedAt      time.Time `json:"created_at"`
	Score          *int64    `json:"score"`
	Sentiment      Sentiment `json:"sentiment"`
	SentimentScore *float64  `json:"sentiment_score"`
	Theme          *string   `json:"theme"`
}

// Content is title + body: the text analyzers actually read.
func (m *Mention) Content() string {
	var parts []string
	if m.Title != nil && *m.Title != "" {
		parts = append(parts, *m.Title)
	}
	if m.Text != "" {
		parts = append(parts, m.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ComputeID derives the stable identifier: the URL when known, otherwise
// source + author + the first 200 characters of content.
func (m *Mention) ComputeID() string {
	basis := ""
	if m.URL != nil && *m.URL != "" {
		basis = *m.URL
	} else {
		author := "None"
		if m.Author != nil {
			author = *m.Author
		}
		basis = m.Source + ":" + author + ":" + truncateRunes(m.Content(), 200)
	}
	sum := sha1.Sum([]byte(basis))
	return hex.EncodeToString(sum[:])[:16]
}

// Normalize fills the ID and anchors the timestamp to UTC. Sources call it
// once per built mention.
func (m *Mention) Normalize() {
	m.CreatedAt = m.CreatedAt.UTC()
	if m.ID == "" {
		m.ID = m.ComputeID()
	}
}

// Str returns a pointer to s, or nil when s is empty.
func Str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Int returns a pointer to v.
func Int(v int64) *int64 { return &v }

// Float returns a pointer to v.
func Float(v float64) *float64 { return &v }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
