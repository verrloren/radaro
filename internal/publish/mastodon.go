package publish

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// MastodonCredentials use a personal access token from Preferences →
// Development → New application (scopes: read, write:statuses).
type MastodonCredentials struct {
	Instance    string `json:"instance"` // e.g. https://mastodon.social
	AccessToken string `json:"access_token"`
}

// Mastodon publishes statuses and replies on the account's instance.
type Mastodon struct {
	Creds MastodonCredentials
}

// MastodonInstanceURL normalizes "mastodon.social" to "https://mastodon.social".
func MastodonInstanceURL(instance string) string {
	instance = strings.TrimRight(strings.TrimSpace(instance), "/")
	if instance == "" {
		instance = "mastodon.social"
	}
	if !strings.HasPrefix(instance, "http://") && !strings.HasPrefix(instance, "https://") {
		instance = "https://" + instance
	}
	return instance
}

func (m *Mastodon) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + m.Creds.AccessToken}
}

type mastodonAccount struct {
	Acct      string `json:"acct"`
	Suspended bool   `json:"suspended"`
}

func (m *Mastodon) verify(ctx context.Context) (mastodonAccount, error) {
	var out mastodonAccount
	err := do(ctx, request{method: "GET", url: MastodonInstanceURL(m.Creds.Instance) + "/api/v1/accounts/verify_credentials", headers: m.auth()}, &out)
	return out, err
}

// VerifyCredentials returns the account's handle (acct).
func (m *Mastodon) VerifyCredentials(ctx context.Context) (string, error) {
	a, err := m.verify(ctx)
	return a.Acct, err
}

// Check verifies the token. A suspended account is refused with 403, or, on
// some instances, still answers with suspended=true.
func (m *Mastodon) Check(ctx context.Context) (Health, error) {
	a, err := m.verify(ctx)
	if err != nil {
		return healthOf(err)
	}
	if a.Suspended {
		return Health{Status: HealthSuspended, Detail: "@" + a.Acct + " is suspended"}, nil
	}
	return Health{Status: HealthLive, Detail: "@" + a.Acct}, nil
}

func (m *Mastodon) Publish(ctx context.Context, p Post) (Result, error) {
	base := MastodonInstanceURL(m.Creds.Instance)
	form := url.Values{"status": {p.Body}, "visibility": {"public"}}
	if p.Kind == "reply" {
		id, err := m.resolveStatus(ctx, p.ReplyTo)
		if err != nil {
			return Result{}, err
		}
		form.Set("in_reply_to_id", id)
	}
	headers := m.auth()
	if p.IdempotencyKey != "" {
		headers["Idempotency-Key"] = p.IdempotencyKey
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := do(ctx, request{method: "POST", url: base + "/api/v1/statuses", headers: headers,
		body: strings.NewReader(form.Encode()), contentType: "application/x-www-form-urlencoded"}, &out); err != nil {
		return Result{}, err
	}
	return Result{RemoteID: out.ID, URL: out.URL}, nil
}

// resolveStatus maps a status URL (possibly on another instance) to the ID
// this instance knows it by, fetching it over federation if needed.
func (m *Mastodon) resolveStatus(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target != "" && !strings.Contains(target, "/") {
		return target, nil // already a local status ID
	}
	params := url.Values{"q": {target}, "type": {"statuses"}, "resolve": {"true"}, "limit": {"1"}}
	var out struct {
		Statuses []struct {
			ID string `json:"id"`
		} `json:"statuses"`
	}
	if err := do(ctx, request{method: "GET", url: MastodonInstanceURL(m.Creds.Instance) + "/api/v2/search?" + params.Encode(), headers: m.auth()}, &out); err != nil {
		return "", err
	}
	if len(out.Statuses) == 0 {
		return "", errors.New("mastodon could not resolve the status to reply to: " + target)
	}
	return out.Statuses[0].ID, nil
}

func (m *Mastodon) Metrics(ctx context.Context, remoteID string) (Metrics, error) {
	var out struct {
		Favourites int64 `json:"favourites_count"`
		Reblogs    int64 `json:"reblogs_count"`
		Replies    int64 `json:"replies_count"`
	}
	err := do(ctx, request{method: "GET", url: MastodonInstanceURL(m.Creds.Instance) + "/api/v1/statuses/" + url.PathEscape(remoteID), headers: m.auth()}, &out)
	if isNotFound(err) {
		return Metrics{MetricRemoved: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return Metrics{"favourites": out.Favourites, "reblogs": out.Reblogs, "replies": out.Replies}, nil
}
