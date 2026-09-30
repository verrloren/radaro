package publish

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// BlueskyCredentials sign in with an app password (Settings → App passwords).
type BlueskyCredentials struct {
	Service     string `json:"service"` // PDS, default https://bsky.social
	Identifier  string `json:"identifier"`
	AppPassword string `json:"app_password"`
}

// Bluesky publishes posts and replies through the account's PDS.
type Bluesky struct {
	Creds BlueskyCredentials
}

type blueskySession struct {
	AccessJwt string `json:"accessJwt"`
	DID       string `json:"did"`
	Handle    string `json:"handle"`
	// Deactivated and taken-down accounts can still sign in; the PDS says
	// so with active=false and a status (takendown, suspended, deactivated).
	Active *bool  `json:"active"`
	Status string `json:"status"`
}

type strongRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

func (b *Bluesky) service() string {
	if b.Creds.Service == "" {
		return "https://bsky.social"
	}
	return strings.TrimRight(b.Creds.Service, "/")
}

// Login creates a session; ConnectBluesky uses it to verify credentials.
func (b *Bluesky) Login(ctx context.Context) (blueskySession, error) {
	var s blueskySession
	body, _ := jsonBody(map[string]string{"identifier": b.Creds.Identifier, "password": b.Creds.AppPassword})
	err := do(ctx, request{method: "POST", url: b.service() + "/xrpc/com.atproto.server.createSession",
		body: body, contentType: "application/json"}, &s)
	if err != nil {
		return s, fmt.Errorf("bluesky login failed: %w", err)
	}
	if s.Active != nil && !*s.Active {
		status := s.Status
		if status == "" {
			status = "inactive"
		}
		return s, &AccountError{Status: HealthSuspended, Detail: "bluesky account @" + s.Handle + " is " + status}
	}
	return s, nil
}

// Check signs in, which is the one call that proves the app password works.
func (b *Bluesky) Check(ctx context.Context) (Health, error) {
	s, err := b.Login(ctx)
	if err != nil {
		return healthOf(err)
	}
	return Health{Status: HealthLive, Detail: "@" + s.Handle}, nil
}

func (b *Bluesky) xrpcGet(ctx context.Context, s blueskySession, method string, params url.Values, out any) error {
	return do(ctx, request{method: "GET", url: b.service() + "/xrpc/" + method + "?" + params.Encode(),
		headers: map[string]string{"Authorization": "Bearer " + s.AccessJwt}}, out)
}

func (b *Bluesky) Publish(ctx context.Context, p Post) (Result, error) {
	s, err := b.Login(ctx)
	if err != nil {
		return Result{}, err
	}
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      p.Body,
		"createdAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if facets := blueskyFacets(p.Body); len(facets) > 0 {
		record["facets"] = facets
	}
	if p.Kind == "reply" {
		root, parent, err := b.replyRefs(ctx, s, p.ReplyTo)
		if err != nil {
			return Result{}, err
		}
		record["reply"] = map[string]any{"root": root, "parent": parent}
	}
	body, _ := jsonBody(map[string]any{"repo": s.DID, "collection": "app.bsky.feed.post", "record": record})
	var created strongRef
	if err := do(ctx, request{method: "POST", url: b.service() + "/xrpc/com.atproto.repo.createRecord", body: body,
		contentType: "application/json", headers: map[string]string{"Authorization": "Bearer " + s.AccessJwt}}, &created); err != nil {
		return Result{}, err
	}
	rkey := created.URI[strings.LastIndex(created.URI, "/")+1:]
	return Result{RemoteID: created.URI, URL: "https://bsky.app/profile/" + s.Handle + "/post/" + rkey}, nil
}

// replyRefs resolves the post being answered into the parent and thread-root references.
func (b *Bluesky) replyRefs(ctx context.Context, s blueskySession, target string) (root, parent strongRef, err error) {
	uri, err := b.postURI(ctx, s, target)
	if err != nil {
		return root, parent, err
	}
	var out struct {
		Posts []struct {
			URI    string `json:"uri"`
			CID    string `json:"cid"`
			Record struct {
				Reply *struct {
					Root strongRef `json:"root"`
				} `json:"reply"`
			} `json:"record"`
		} `json:"posts"`
	}
	if err := b.xrpcGet(ctx, s, "app.bsky.feed.getPosts", url.Values{"uris": {uri}}, &out); err != nil {
		return root, parent, err
	}
	if len(out.Posts) == 0 {
		return root, parent, fmt.Errorf("bluesky post not found: %s", target)
	}
	post := out.Posts[0]
	parent = strongRef{URI: post.URI, CID: post.CID}
	root = parent
	if post.Record.Reply != nil && post.Record.Reply.Root.URI != "" {
		root = post.Record.Reply.Root
	}
	return root, parent, nil
}

var blueskyPostURL = regexp.MustCompile(`^https://bsky\.app/profile/([^/]+)/post/([^/?#]+)`)

// postURI turns a bsky.app link (or an at:// URI) into an at:// post URI.
func (b *Bluesky) postURI(ctx context.Context, s blueskySession, target string) (string, error) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "at://") {
		return target, nil
	}
	m := blueskyPostURL.FindStringSubmatch(target)
	if m == nil {
		return "", fmt.Errorf("not a Bluesky post link: %s", target)
	}
	actor, rkey := m[1], m[2]
	if !strings.HasPrefix(actor, "did:") {
		var out struct {
			DID string `json:"did"`
		}
		if err := b.xrpcGet(ctx, s, "com.atproto.identity.resolveHandle", url.Values{"handle": {actor}}, &out); err != nil {
			return "", err
		}
		actor = out.DID
	}
	return "at://" + actor + "/app.bsky.feed.post/" + rkey, nil
}

func (b *Bluesky) Metrics(ctx context.Context, remoteID string) (Metrics, error) {
	s, err := b.Login(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Posts []struct {
			LikeCount   int64 `json:"likeCount"`
			RepostCount int64 `json:"repostCount"`
			ReplyCount  int64 `json:"replyCount"`
			QuoteCount  int64 `json:"quoteCount"`
		} `json:"posts"`
	}
	if err := b.xrpcGet(ctx, s, "app.bsky.feed.getPosts", url.Values{"uris": {remoteID}}, &out); err != nil {
		return nil, err
	}
	if len(out.Posts) == 0 {
		return Metrics{MetricRemoved: true}, nil
	}
	p := out.Posts[0]
	return Metrics{"likes": p.LikeCount, "reposts": p.RepostCount, "replies": p.ReplyCount, "quotes": p.QuoteCount}, nil
}

var (
	facetURL = regexp.MustCompile(`https?://[^\s<>"]+`)
	facetTag = regexp.MustCompile(`(^|\s)(#[\p{L}\p{N}_]+)`)
)

// blueskyFacets marks links and hashtags; without facets Bluesky renders them
// as plain text. Offsets are UTF-8 byte offsets, which Go strings index by.
func blueskyFacets(text string) []map[string]any {
	var facets []map[string]any
	for _, loc := range facetURL.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		for end > start && strings.ContainsRune(".,;:!?)]}'\"", rune(text[end-1])) {
			end-- // trailing punctuation belongs to the sentence
		}
		facets = append(facets, map[string]any{
			"index":    map[string]int{"byteStart": start, "byteEnd": end},
			"features": []map[string]string{{"$type": "app.bsky.richtext.facet#link", "uri": text[start:end]}},
		})
	}
	for _, loc := range facetTag.FindAllStringSubmatchIndex(text, -1) {
		start, end := loc[4], loc[5]
		facets = append(facets, map[string]any{
			"index":    map[string]int{"byteStart": start, "byteEnd": end},
			"features": []map[string]string{{"$type": "app.bsky.richtext.facet#tag", "tag": text[start+1 : end]}},
		})
	}
	return facets
}
