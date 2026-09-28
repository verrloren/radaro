package sources

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// Mastodon searches statuses on one instance. Full-text search is
// instance-dependent and normally requires a user access token.
type Mastodon struct {
	Instance    string
	AccessToken string
}

// NewMastodon defaults the instance to mastodon.social and strips the scheme.
func NewMastodon(instance, token string) *Mastodon {
	if instance == "" {
		instance = "mastodon.social"
	}
	instance = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(instance, "https://"), "http://"), "/")
	return &Mastodon{Instance: instance, AccessToken: token}
}

func (s *Mastodon) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	perPage := minInt(limit, 40)
	params := url.Values{
		"q":     {query},
		"type":  {"statuses"},
		"limit": {strconv.Itoa(perPage)},
	}
	if cursor != "" {
		params.Set("max_id", cursor)
	}
	var headers map[string]string
	if s.AccessToken != "" {
		headers = map[string]string{"Authorization": "Bearer " + s.AccessToken}
	}
	var data struct {
		Statuses []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			URL     string `json:"url"`
			Created string `json:"created_at"`
			Favs    *int64 `json:"favourites_count"`
			Account struct {
				Acct string `json:"acct"`
			} `json:"account"`
		} `json:"statuses"`
	}
	if err := getJSON(ctx, "https://"+s.Instance+"/api/v2/search", params, headers, &data); err != nil {
		return Page{}, err
	}

	var mentions []model.Mention
	for _, st := range data.Statuses {
		m := model.Mention{
			Source:    "mastodon",
			Query:     query,
			Author:    model.Str(st.Account.Acct),
			Text:      StripHTML(st.Content),
			URL:       model.Str(st.URL),
			CreatedAt: parseTime(st.Created),
			Score:     st.Favs,
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	next := ""
	if len(data.Statuses) >= perPage {
		next = data.Statuses[len(data.Statuses)-1].ID
	}
	return Page{Mentions: mentions, NextCursor: next}, nil
}
