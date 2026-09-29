package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			if readJSON(t, r)["password"] != "app-pw" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"AuthenticationRequired","message":"Invalid identifier or password"}`))
				return
			}
			w.Write([]byte(`{"accessJwt":"jwt","did":"did:plc:me","handle":"me.bsky.social"}`))
		case "/api/v1/accounts/verify_credentials":
			if r.Header.Get("Authorization") != "Bearer masto-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"acct":"me"}`))
		case "/users/me":
			if r.Header.Get("api-key") != "devto-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"username":"me_dev"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	old := devtoAPI
	devtoAPI = srv.URL
	defer func() { devtoAPI = old }()
	ctx := context.Background()

	handle, creds, err := Connect(ctx, "bluesky", ConnectInput{Handle: "me", Service: srv.URL, Secret: " app-pw "})
	if err != nil || handle != "me.bsky.social" || creds.(BlueskyCredentials).AppPassword != "app-pw" {
		t.Fatalf("bluesky %q %+v %v", handle, creds, err)
	}
	if _, _, err := Connect(ctx, "bluesky", ConnectInput{Handle: "me", Service: srv.URL, Secret: "wrong"}); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("bluesky bad password: %v", err)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	if handle, _, err = Connect(ctx, "mastodon", ConnectInput{Instance: srv.URL, Secret: "masto-token"}); err != nil || handle != "me@"+host {
		t.Fatalf("mastodon %q %v", handle, err)
	}
	if _, _, err := Connect(ctx, "mastodon", ConnectInput{Instance: srv.URL, Secret: "nope"}); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("mastodon bad token: %v", err)
	}
	if handle, _, err = Connect(ctx, "devto", ConnectInput{Secret: "devto-key"}); err != nil || handle != "me_dev" {
		t.Fatalf("devto %q %v", handle, err)
	}
	for _, c := range []struct {
		platform string
		in       ConnectInput
	}{{"devto", ConnectInput{}}, {"bluesky", ConnectInput{Secret: "x"}}, {"reddit", ConnectInput{Secret: "x"}}, {"myspace", ConnectInput{Secret: "x"}}} {
		if _, _, err := Connect(ctx, c.platform, c.in); err == nil {
			t.Fatalf("%s %+v accepted", c.platform, c.in)
		}
	}
}
