package sources

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// The official AppView host exposes the public searchPosts XRPC endpoint
// without a session.
var blueskyAPI = "https://api.bsky.app/xrpc/app.bsky.feed.searchPosts"

// Bluesky searches posts via the public AT Protocol endpoint — no auth needed.
type Bluesky struct{}

func (s *Bluesky) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	params := url.Values{
		"q":     {query},
		"limit": {strconv.Itoa(minInt(limit, 100))},
		"sort":  {"latest"},
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	if !since.IsZero() {
		params.Set("since", rfc3339(since))
	}
	var data struct {
		Posts []struct {
			URI    string `json:"uri"`
			Author struct {
				Handle string `json:"handle"`
			} `json:"author"`
			Record struct {
				Text      string `json:"text"`
				CreatedAt string `json:"createdAt"`
			} `json:"record"`
			IndexedAt string `json:"indexedAt"`
			LikeCount *int64 `json:"likeCount"`
		} `json:"posts"`
		Cursor string `json:"cursor"`
	}
	if err := getJSON(ctx, blueskyAPI, params, nil, &data); err != nil {
		// The anonymous AppView refuses cursor pages (HTTP 403): older history
		// is not reachable without a session, so treat it as the end.
		var he *HTTPError
		if cursor != "" && errors.As(err, &he) && he.Status == http.StatusForbidden {
			return Page{}, nil
		}
		return Page{}, err
	}

	var mentions []model.Mention
	for _, post := range data.Posts {
		handle := post.Author.Handle
		rkey := ""
		if post.URI != "" {
			rkey = post.URI[strings.LastIndex(post.URI, "/")+1:]
		}
		var link *string
		if handle != "" && rkey != "" {
			link = model.Str("https://bsky.app/profile/" + handle + "/post/" + rkey)
		}
		created := post.IndexedAt
		if created == "" {
			created = post.Record.CreatedAt
		}
		m := model.Mention{
			Source:    "bluesky",
			Query:     query,
			Author:    model.Str(handle),
			Text:      post.Record.Text,
			URL:       link,
			CreatedAt: parseTime(created),
			Score:     post.LikeCount,
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	return Page{Mentions: mentions, NextCursor: data.Cursor}, nil
}
