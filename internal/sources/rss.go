package sources

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/netproxy"
)

// RSS filters entries of configured RSS/Atom feeds (blogs, news, Google Alerts
// RSS) to those mentioning the query. It has no pagination.
type RSS struct {
	Feeds []string
}

func (s *RSS) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	if len(s.Feeds) == 0 {
		return Page{}, errors.New("RSS requires at least one URL in RADARO_RSS_FEEDS")
	}
	needle := strings.ToLower(query)
	parser := gofeed.NewParser()
	parser.UserAgent = UserAgent
	parser.Client = &http.Client{Timeout: 15 * time.Second, Transport: netproxy.Transport()}

	var mentions []model.Mention
	for _, feedURL := range s.Feeds {
		feed, err := parser.ParseURLWithContext(feedURL, ctx)
		if err != nil {
			continue // one bad or slow feed shouldn't cost the others their fetch
		}
		for _, item := range feed.Items {
			summary := item.Description
			if !strings.Contains(strings.ToLower(item.Title+" "+summary), needle) {
				continue
			}
			created := time.Now().UTC()
			if item.PublishedParsed != nil {
				created = item.PublishedParsed.UTC()
			} else if item.UpdatedParsed != nil {
				created = item.UpdatedParsed.UTC()
			}
			author := ""
			if len(item.Authors) > 0 && item.Authors[0] != nil {
				author = item.Authors[0].Name
			}
			m := model.Mention{
				Source:    "rss",
				Query:     query,
				Author:    model.Str(author),
				Title:     model.Str(item.Title),
				Text:      StripHTML(summary),
				URL:       model.Str(item.Link),
				CreatedAt: created,
			}
			m.Normalize()
			mentions = append(mentions, m)
		}
	}
	if len(mentions) > limit {
		mentions = mentions[:limit]
	}
	return Page{Mentions: mentions}, nil
}
