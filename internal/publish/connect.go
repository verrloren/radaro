package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ConnectInput is what a user supplies to connect an account on a platform
// that takes a key (Reddit uses the OAuth flow instead).
type ConnectInput struct {
	Handle   string // bluesky: handle or email
	Service  string // bluesky: PDS, default https://bsky.social
	Instance string // mastodon: instance, default mastodon.social
	Secret   string // bluesky app password, mastodon access token, dev.to API key
}

// Connect checks the credentials with the platform and returns the handle to
// save the account under and the credentials to store.
func Connect(ctx context.Context, platform string, in ConnectInput) (string, any, error) {
	in.Secret = strings.TrimSpace(in.Secret)
	if in.Secret == "" {
		return "", nil, errors.New("a key or password is required")
	}
	switch platform {
	case "bluesky":
		if strings.TrimSpace(in.Handle) == "" {
			return "", nil, errors.New("handle is required (e.g. you.bsky.social)")
		}
		creds := BlueskyCredentials{Service: strings.TrimSpace(in.Service), Identifier: strings.TrimSpace(in.Handle), AppPassword: in.Secret}
		session, err := (&Bluesky{Creds: creds}).Login(ctx)
		if err != nil {
			return "", nil, err
		}
		return session.Handle, creds, nil
	case "mastodon":
		instance := strings.TrimSpace(in.Instance)
		if instance == "" {
			instance = "mastodon.social"
		}
		creds := MastodonCredentials{Instance: MastodonInstanceURL(instance), AccessToken: in.Secret}
		acct, err := (&Mastodon{Creds: creds}).VerifyCredentials(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("mastodon rejected the token: %w", err)
		}
		host := strings.TrimPrefix(strings.TrimPrefix(creds.Instance, "https://"), "http://")
		return acct + "@" + host, creds, nil
	case "devto":
		creds := DevtoCredentials{APIKey: in.Secret}
		user, err := (&Devto{Creds: creds}).Me(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("dev.to rejected the key: %w", err)
		}
		return user, creds, nil
	case "reddit":
		return "", nil, errors.New("reddit connects through the browser sign-in")
	}
	return "", nil, fmt.Errorf("unknown platform: %s", platform)
}
