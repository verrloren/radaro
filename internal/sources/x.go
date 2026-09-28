package sources

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

var xAPI = "https://api.x.com/2/tweets/search/recent"

// X searches posts via the keyed X API v2 recent-search endpoint.
type X struct {
	BearerToken string
}

func (s *X) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	if s.BearerToken == "" {
		return Page{}, errors.New("X requires RADARO_X_BEARER_TOKEN")
	}
	// X requires 10 <= max_results <= 100.
	params := url.Values{
		"query":        {query},
		"max_results":  {strconv.Itoa(max(10, minInt(limit, 100)))},
		"tweet.fields": {"created_at,public_metrics,author_id"},
		"expansions":   {"author_id"},
		"user.fields":  {"username,name"},
	}
	if cursor != "" {
		params.Set("next_token", cursor)
	}
	if start := xRecentStart(since, time.Now()); start != "" {
		params.Set("start_time", start)
	}
	var data struct {
		Data []struct {
			ID            string `json:"id"`
			Text          string `json:"text"`
			AuthorID      string `json:"author_id"`
			CreatedAt     string `json:"created_at"`
			PublicMetrics struct {
				LikeCount *int64 `json:"like_count"`
			} `json:"public_metrics"`
		} `json:"data"`
		Includes struct {
			Users []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
				Name     string `json:"name"`
			} `json:"users"`
		} `json:"includes"`
		Meta struct {
			NextToken string `json:"next_token"`
		} `json:"meta"`
	}
	if err := getJSON(ctx, xAPI, params, map[string]string{"Authorization": "Bearer " + s.BearerToken}, &data); err != nil {
		return Page{}, err
	}

	type user struct{ username, name string }
	users := map[string]user{}
	for _, u := range data.Includes.Users {
		users[u.ID] = user{u.Username, u.Name}
	}
	// Keep every returned post: X enforces a 10-row minimum and next_token
	// advances past all of them, so trimming would silently drop rows.
	var mentions []model.Mention
	for _, post := range data.Data {
		if post.ID == "" {
			continue
		}
		u := users[post.AuthorID]
		author := u.username
		link := "https://x.com/i/web/status/" + post.ID
		if u.username != "" {
			link = "https://x.com/" + u.username + "/status/" + post.ID
		} else {
			author = u.name
		}
		m := model.Mention{
			Source:    "x",
			Query:     query,
			Author:    model.Str(author),
			Text:      post.Text,
			URL:       model.Str(link),
			CreatedAt: parseTime(post.CreatedAt),
			Score:     post.PublicMetrics.LikeCount,
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	return Page{Mentions: mentions, NextCursor: data.Meta.NextToken}, nil
}

// xRecentStart returns a provider-valid recent-search boundary (within the
// last 7 days and at least 30s old), otherwise "" to use X's default window.
func xRecentStart(since, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	if since.After(now.Add(-7*24*time.Hour)) && since.Before(now.Add(-30*time.Second)) {
		return rfc3339(since)
	}
	return ""
}
