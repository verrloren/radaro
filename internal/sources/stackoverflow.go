package sources

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

var stackExchangeAPI = "https://api.stackexchange.com/2.3/search/advanced"

// Backoff and the anonymous daily quota apply to the whole process, even
// though the pipeline builds a fresh source object for each scan.
var stackExchangeState struct {
	sync.Mutex
	backoffUntil   time.Time
	quotaRemaining int // -1 = unknown
}

func init() { stackExchangeState.quotaRemaining = -1 }

// StackOverflow searches questions via the public Stack Exchange API.
type StackOverflow struct{}

func (s *StackOverflow) FetchPage(ctx context.Context, query string, limit int, cursor string, since time.Time) (Page, error) {
	stackExchangeState.Lock()
	wait := time.Until(stackExchangeState.backoffUntil)
	quota := stackExchangeState.quotaRemaining
	stackExchangeState.Unlock()
	if wait > 0 {
		return Page{}, fmt.Errorf("stack exchange API requested backoff (%.0fs remaining)", wait.Seconds())
	}
	if quota == 0 {
		return Page{}, fmt.Errorf("stack exchange API daily quota is exhausted")
	}

	params := url.Values{
		"site":     {"stackoverflow"},
		"q":        {query},
		"pagesize": {strconv.Itoa(minInt(limit, 100))},
		"sort":     {"creation"},
		"order":    {"desc"},
		"filter":   {"withbody"},
	}
	if cursor != "" {
		params.Set("todate", cursor)
	}
	if !since.IsZero() {
		params.Set("fromdate", strconv.FormatInt(since.Unix(), 10))
	}
	var data struct {
		Items []struct {
			QuestionID   int64  `json:"question_id"`
			Title        string `json:"title"`
			Body         string `json:"body"`
			Link         string `json:"link"`
			Score        *int64 `json:"score"`
			CreationDate int64  `json:"creation_date"`
			Owner        struct {
				DisplayName string `json:"display_name"`
			} `json:"owner"`
		} `json:"items"`
		HasMore        bool `json:"has_more"`
		Backoff        *int `json:"backoff"`
		QuotaRemaining *int `json:"quota_remaining"`
	}
	if err := getJSON(ctx, stackExchangeAPI, params, nil, &data); err != nil {
		return Page{}, err
	}
	stackExchangeState.Lock()
	if data.Backoff != nil {
		stackExchangeState.backoffUntil = time.Now().Add(time.Duration(max(*data.Backoff, 0)) * time.Second)
	}
	if data.QuotaRemaining != nil {
		stackExchangeState.quotaRemaining = max(*data.QuotaRemaining, 0)
	}
	stackExchangeState.Unlock()

	var mentions []model.Mention
	var oldest int64
	for _, q := range data.Items {
		if q.CreationDate != 0 && (oldest == 0 || q.CreationDate < oldest) {
			oldest = q.CreationDate
		}
		link := q.Link
		if link == "" && q.QuestionID != 0 {
			link = fmt.Sprintf("https://stackoverflow.com/questions/%d", q.QuestionID)
		}
		m := model.Mention{
			Source:    "stackoverflow",
			Query:     query,
			Author:    model.Str(StripHTML(q.Owner.DisplayName)),
			Title:     model.Str(StripHTML(q.Title)),
			Text:      matchingExcerpt(StripHTML(q.Body), query, 800),
			URL:       model.Str(link),
			CreatedAt: unixTime(q.CreationDate),
			Score:     q.Score,
		}
		m.Normalize()
		mentions = append(mentions, m)
	}
	// todate is inclusive; the cursor sits at the page's oldest second so
	// questions sharing it are not lost. The store de-duplicates the overlap.
	// has_more is unreliable once todate is set (the API reports false for a
	// full page), so a full page also counts as "there may be more".
	next := ""
	if (data.HasMore || len(data.Items) >= minInt(limit, 100)) && oldest != 0 {
		next = strconv.FormatInt(oldest, 10)
	}
	return Page{Mentions: mentions, NextCursor: next}, nil
}

// matchingExcerpt keeps the matched term visible instead of storing an entire long question.
func matchingExcerpt(text, query string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	start := 0
	if idx := strings.Index(strings.ToLower(text), strings.ToLower(query)); idx >= 0 {
		start = max(0, len([]rune(text[:idx]))-140)
	}
	end := min(len(runes), start+limit)
	excerpt := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt
}
