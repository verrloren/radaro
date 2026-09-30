package publish

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultRetryAfter is used when a platform says "slow down" without saying
// for how long.
const defaultRetryAfter = time.Minute

// clock is replaced in tests that parse absolute reset times.
var clock = time.Now

// responseError turns a non-2xx response into a RateLimitError, an
// AccountError, or an APIError when it says nothing about the account.
func responseError(status int, h http.Header, raw []byte) error {
	code, msg := errorMessage(raw)
	detail := msg
	if detail == "" {
		detail = fmt.Sprintf("HTTP %d", status)
	}
	if status == http.StatusTooManyRequests {
		return &RateLimitError{RetryAfter: retryAfter(h), Detail: detail}
	}
	if s := accountStatus(status, code, msg); s != "" {
		return &AccountError{Status: s, Detail: detail}
	}
	return &APIError{Status: status, Body: snippet(raw)}
}

// Bluesky names account refusals in the XRPC error field.
var suspendedCodes = map[string]bool{"AccountTakedown": true, "AccountDeactivated": true, "AccountSuspended": true}

// Mastodon only says it in prose ("Your login is currently disabled").
var suspendedWords = []string{"suspended", "disabled", "takedown", "taken down", "deactivated"}

func accountStatus(status int, code, msg string) string {
	if status >= 400 && status < 500 && suspendedCodes[code] {
		return HealthSuspended
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		low := strings.ToLower(msg)
		for _, w := range suspendedWords {
			if strings.Contains(low, w) {
				return HealthSuspended
			}
		}
	}
	// A 403 alone is too broad (a banned subreddit, a private community) to
	// call the account dead.
	if status == http.StatusUnauthorized {
		return HealthInvalid
	}
	return ""
}

// errorMessage pulls the error code and a readable message out of a JSON
// error envelope; msg falls back to a snippet of the raw body.
func errorMessage(raw []byte) (code, msg string) {
	var env map[string]any
	if json.Unmarshal(raw, &env) != nil {
		return "", snippet(raw)
	}
	code, _ = env["error"].(string)
	text, _ := env["message"].(string)
	if text == "" {
		text, _ = env["error_description"].(string)
	}
	switch {
	case code != "" && text != "":
		return code, snippet([]byte(code + ": " + text))
	case code != "":
		return code, snippet([]byte(code))
	case text != "":
		return "", snippet([]byte(text))
	}
	return "", snippet(raw)
}

// retryAfter reads how long a 429 asks us to wait.
func retryAfter(h http.Header) time.Duration {
	now := clock()
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			return seconds(n)
		}
		if t, err := http.ParseTime(v); err == nil && t.After(now) {
			return t.Sub(now)
		}
	}
	for _, k := range []string{"RateLimit-Reset", "X-RateLimit-Reset"} {
		if d := resetIn(h.Get(k), now); d > 0 {
			return d
		}
	}
	return defaultRetryAfter
}

// resetIn reads a reset header. Bluesky sends a unix time, Mastodon an ISO
// time and Reddit the seconds left, so a small number is a delay.
func resetIn(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		if n > 1e9 {
			return time.Unix(0, int64(n*1e9)).Sub(now)
		}
		return seconds(n)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Sub(now)
	}
	return 0
}

func seconds(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }

// healthOf reports a failed check as an account status; errors that say
// nothing about the account (network, 5xx) stay errors.
func healthOf(err error) (Health, error) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return Health{Status: HealthLimited, Detail: rl.Detail, RetryAfter: rl.RetryAfter}, nil
	}
	var ae *AccountError
	if errors.As(err, &ae) {
		return Health{Status: ae.Status, Detail: ae.Detail}, nil
	}
	return Health{}, err
}

// isNotFound reports a 404 or 410, i.e. the post is gone.
func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusGone)
}
