package sources

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/redditbrowser"
)

var (
	redditAPI      = "https://oauth.reddit.com/search"
	redditTokenAPI = "https://www.reddit.com/api/v1/access_token"
)

// Reddit searches posts via the OAuth Data API. Anonymous JSON search is no
// longer reliable, so it needs an app client or an existing bearer token.
type Reddit struct {
	ClientID     string
	ClientSecret string // empty for an "installed app" with a refresh token
	RefreshToken string // from a connected account; searches as that user
	AccessToken  string // minted once and reused across backfill pages
	Browser      *redditbrowser.Credentials
}

func (s *Reddit) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	if s.Browser != nil {
		page, err := redditbrowser.Search(ctx, *s.Browser, query, cursor, minInt(limit, 100))
		if err != nil {
			return Page{}, err
		}
		out := Page{NextCursor: page.Next, Mentions: []model.Mention{}}
		for _, d := range page.Mentions {
			if !since.IsZero() && !d.CreatedAt.IsZero() && d.CreatedAt.Before(since) {
				continue
			}
			m := model.Mention{Source: "reddit", Query: query, Author: model.Str(d.Author), Title: model.Str(d.Title), Text: d.Body, URL: model.Str(d.URL), CreatedAt: d.CreatedAt, Score: d.Score, Reddit: &model.RedditStats{Community: d.Community, Comments: d.Comments}}
			m.Normalize()
			out.Mentions = append(out.Mentions, m)
		}
		return out, nil
	}
	if s.AccessToken == "" {
		token, err := s.appToken(ctx)
		if err != nil {
			return Page{}, err
		}
		s.AccessToken = token
	}
	params := url.Values{
		"q":     {query},
		"limit": {strconv.Itoa(minInt(limit, 100))},
		"sort":  {"new"},
		"type":  {"link"},
	}
	if cursor != "" {
		params.Set("after", cursor)
	}
	var data struct {
		Data struct {
			Children []struct {
				Data struct {
					Author      string   `json:"author"`
					Title       string   `json:"title"`
					Selftext    string   `json:"selftext"`
					Permalink   string   `json:"permalink"`
					URL         string   `json:"url"`
					CreatedUTC  float64  `json:"created_utc"`
					Score       *float64 `json:"score"`
					Subreddit   string   `json:"subreddit"`
					NumComments *int64   `json:"num_comments"`
					UpvoteRatio *float64 `json:"upvote_ratio"`
				} `json:"data"`
			} `json:"children"`
			After string `json:"after"`
		} `json:"data"`
	}
	headers := map[string]string{"Authorization": "Bearer " + s.AccessToken}
	if err := getJSON(ctx, redditAPI, params, headers, &data); err != nil {
		return Page{}, err
	}

	var mentions []model.Mention
	for _, child := range data.Data.Children {
		d := child.Data
		link := d.URL
		if d.Permalink != "" {
			link = "https://www.reddit.com" + d.Permalink
		}
		var score *int64
		if d.Score != nil {
			score = model.Int(int64(math.Round(*d.Score)))
		}
		m := model.Mention{
			Source:    "reddit",
			Query:     query,
			Author:    model.Str(d.Author),
			Title:     model.Str(d.Title),
			Text:      d.Selftext,
			URL:       model.Str(link),
			CreatedAt: unixTime(int64(d.CreatedUTC)),
			Score:     score,
			Reddit:    &model.RedditStats{Community: d.Subreddit, Comments: d.NumComments, UpvoteRatio: d.UpvoteRatio},
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	return Page{Mentions: mentions, NextCursor: data.Data.After}, nil
}

func (s *Reddit) appToken(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	switch {
	case s.ClientID != "" && s.RefreshToken != "":
		form = url.Values{"grant_type": {"refresh_token"}, "refresh_token": {s.RefreshToken}}
	case s.ClientID == "" || s.ClientSecret == "":
		return "", errors.New("reddit requires OAuth: connect a Reddit account in Setup, or set RADARO_REDDIT_CLIENT_ID and RADARO_REDDIT_CLIENT_SECRET")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, redditTokenAPI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(s.ClientID, s.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := doJSON(req, nil, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("reddit OAuth response did not contain an access token")
	}
	return out.AccessToken, nil
}
