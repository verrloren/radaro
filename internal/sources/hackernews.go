package sources

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

var hackerNewsAPI = "https://hn.algolia.com/api/v1/search_by_date"

// HackerNews searches stories and comments via the public Algolia API — no key required.
type HackerNews struct{}

func (s *HackerNews) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	perPage := minInt(limit, 100)
	params := url.Values{
		"query":         {query},
		"tags":          {"(story,comment)"},
		"hitsPerPage":   {strconv.Itoa(perPage)},
		"typoTolerance": {"false"},
	}
	var bounds []string
	if cursor != "" {
		// Inclusive (<=) so items sharing the previous page's oldest second are
		// not skipped when the page cap splits that group; the store
		// de-duplicates the one-second overlap.
		bounds = append(bounds, "created_at_i<="+cursor)
	}
	if !since.IsZero() {
		bounds = append(bounds, fmt.Sprintf("created_at_i>%d", since.Unix()))
	}
	if len(bounds) > 0 {
		params.Set("numericFilters", strings.Join(bounds, ","))
	}

	var data struct {
		Hits []struct {
			ObjectID    string `json:"objectID"`
			CreatedAtI  int64  `json:"created_at_i"`
			Title       string `json:"title"`
			StoryTitle  string `json:"story_title"`
			CommentText string `json:"comment_text"`
			StoryText   string `json:"story_text"`
			Author      string `json:"author"`
			URL         string `json:"url"`
			Points      *int64 `json:"points"`
		} `json:"hits"`
		Page    int  `json:"page"`
		NbPages *int `json:"nbPages"`
	}
	if err := getJSON(ctx, hackerNewsAPI, params, nil, &data); err != nil {
		return Page{}, err
	}

	var mentions []model.Mention
	var oldest int64
	needle := strings.ToLower(query)
	for _, hit := range data.Hits {
		if hit.CreatedAtI != 0 && (oldest == 0 || hit.CreatedAtI < oldest) {
			oldest = hit.CreatedAtI
		}
		title := hit.Title
		if title == "" {
			title = hit.StoryTitle
		}
		body := hit.CommentText
		if body == "" {
			body = hit.StoryText
		}
		body = StripHTML(body)
		if !strings.Contains(strings.ToLower(title+" "+body), needle) {
			continue
		}
		link := hit.URL
		if hit.ObjectID != "" {
			link = "https://news.ycombinator.com/item?id=" + hit.ObjectID
		}
		m := model.Mention{
			Source:    "hackernews",
			Query:     query,
			Author:    model.Str(hit.Author),
			Title:     model.Str(title),
			Text:      body,
			URL:       model.Str(link),
			CreatedAt: unixTime(hit.CreatedAtI),
			Score:     hit.Points,
		}
		m.Normalize()
		mentions = append(mentions, m)
	}

	hasMore := len(data.Hits) >= perPage
	if data.NbPages != nil {
		hasMore = data.Page+1 < *data.NbPages
	}
	next := ""
	if hasMore && oldest != 0 {
		next = strconv.FormatInt(oldest, 10)
	}
	return Page{Mentions: mentions, NextCursor: next}, nil
}
