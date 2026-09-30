package publish

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Health statuses; the values match the store's account statuses.
const (
	HealthLive      = "live"
	HealthInvalid   = "invalid"
	HealthSuspended = "suspended"
	HealthLimited   = "limited"
)

// Health is the result of an account check.
type Health struct {
	Status string // HealthLive, HealthInvalid, HealthSuspended, HealthLimited
	Detail string // short, human-readable, never contains a secret
	// RetryAfter is set with HealthLimited.
	RetryAfter time.Duration
}

// Checker verifies that an account's credentials work and that the platform
// has not suspended it. A non-nil error means the check itself could not run
// (network, 5xx); the account's status is then unknown, not dead.
type Checker interface {
	Check(ctx context.Context) (Health, error)
}

// RateLimitError is a platform refusing because of a rate limit. Nothing was
// published, so the draft can be retried after RetryAfter.
type RateLimitError struct {
	RetryAfter time.Duration
	Detail     string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited, retry in %s: %s", e.RetryAfter.Round(time.Second), e.Detail)
}

// AccountError is a platform refusing because of the account itself.
type AccountError struct {
	Status string // HealthInvalid or HealthSuspended
	Detail string
}

func (e *AccountError) Error() string { return e.Status + ": " + e.Detail }

// Classify maps a Publish, Metrics or Check error to an account status:
// HealthLimited with a delay, HealthInvalid, HealthSuspended, or "" when the
// error says nothing about the account.
func Classify(err error) (status string, retryAfter time.Duration) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return HealthLimited, rl.RetryAfter
	}
	var ae *AccountError
	if errors.As(err, &ae) {
		return ae.Status, 0
	}
	return "", 0
}

// MetricRemoved is set to true in Metrics when the post is gone (removed by
// moderators, deleted, or taken down).
const MetricRemoved = "removed"

// Limits are the self-imposed publishing limits of one account.
type Limits struct {
	Daily             int           // publications per rolling 24 hours
	MinInterval       time.Duration // between two publications
	CommunityCooldown time.Duration // between two publications in one community (0 = none)
}

// DefaultLimits are conservative per-account defaults for each platform.
func DefaultLimits(platform string) Limits {
	switch platform {
	case "reddit":
		return Limits{Daily: 5, MinInterval: 10 * time.Minute, CommunityCooldown: 24 * time.Hour}
	case "bluesky", "mastodon":
		return Limits{Daily: 20, MinInterval: 2 * time.Minute}
	case "devto":
		return Limits{Daily: 2, MinInterval: time.Hour}
	}
	return Limits{Daily: 10, MinInterval: 5 * time.Minute}
}

// SubredditRule is one rule from a subreddit's rules page.
type SubredditRule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var (
	_ Checker = (*Reddit)(nil)
	_ Checker = (*Bluesky)(nil)
	_ Checker = (*Mastodon)(nil)
	_ Checker = (*Devto)(nil)
)
