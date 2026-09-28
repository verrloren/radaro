package sources

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

var youTubeAPI = "https://www.googleapis.com/youtube/v3/search"

// YouTube searches videos via the keyed YouTube Data API v3.
type YouTube struct {
	APIKey string
}

func (s *YouTube) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	if s.APIKey == "" {
		return Page{}, errors.New("YouTube requires RADARO_YOUTUBE_API_KEY")
	}
	params := url.Values{
		"part":       {"snippet"},
		"q":          {query},
		"type":       {"video"},
		"order":      {"date"},
		"maxResults": {strconv.Itoa(minInt(limit, 50))},
	}
	if cursor != "" {
		params.Set("pageToken", cursor)
	}
	if !since.IsZero() {
		params.Set("publishedAfter", rfc3339(since))
	}
	var data struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet struct {
				ChannelTitle string `json:"channelTitle"`
				Title        string `json:"title"`
				Description  string `json:"description"`
				PublishedAt  string `json:"publishedAt"`
			} `json:"snippet"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	// The header form keeps the key out of URLs, proxy logs and error strings.
	if err := getJSON(ctx, youTubeAPI, params, map[string]string{"X-Goog-Api-Key": s.APIKey}, &data); err != nil {
		return Page{}, err
	}

	var mentions []model.Mention
	for _, item := range data.Items {
		if item.ID.VideoID == "" {
			continue
		}
		sn := item.Snippet
		m := model.Mention{
			Source:    "youtube",
			Query:     query,
			Author:    model.Str(StripHTML(sn.ChannelTitle)),
			Title:     model.Str(StripHTML(sn.Title)),
			Text:      StripHTML(sn.Description),
			URL:       model.Str("https://www.youtube.com/watch?v=" + item.ID.VideoID),
			CreatedAt: parseTime(sn.PublishedAt),
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	return Page{Mentions: mentions, NextCursor: data.NextPageToken}, nil
}
