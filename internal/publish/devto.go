package publish

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

var devtoAPI = "https://dev.to/api"

// DevtoCredentials use an API key from Settings → Extensions → DEV Community API Keys.
type DevtoCredentials struct {
	APIKey string `json:"api_key"`
}

// Devto publishes articles. It does not support replies.
type Devto struct {
	Creds DevtoCredentials
}

func (d *Devto) headers() map[string]string {
	return map[string]string{"api-key": d.Creds.APIKey, "Accept": "application/vnd.forem.api-v1+json"}
}

// Me returns the account's username.
func (d *Devto) Me(ctx context.Context) (string, error) {
	var out struct {
		Username string `json:"username"`
	}
	err := do(ctx, request{method: "GET", url: devtoAPI + "/users/me", headers: d.headers()}, &out)
	return out.Username, err
}

func (d *Devto) Publish(ctx context.Context, p Post) (Result, error) {
	if p.Kind == "reply" {
		return Result{}, errors.New("dev.to does not support replies")
	}
	var tags []string
	for _, t := range strings.Split(p.Community, ",") {
		if t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))); t != "" {
			tags = append(tags, t)
		}
	}
	if len(tags) > 4 {
		return Result{}, errors.New("dev.to allows at most 4 tags")
	}
	article := map[string]any{"title": p.Title, "body_markdown": p.Body, "published": true}
	if len(tags) > 0 {
		article["tags"] = tags
	}
	body, _ := jsonBody(map[string]any{"article": article})
	var out struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := do(ctx, request{method: "POST", url: devtoAPI + "/articles", headers: d.headers(), body: body, contentType: "application/json"}, &out); err != nil {
		return Result{}, err
	}
	return Result{RemoteID: strconv.FormatInt(out.ID, 10), URL: out.URL}, nil
}

func (d *Devto) Metrics(ctx context.Context, remoteID string) (Metrics, error) {
	var articles []struct {
		ID        int64 `json:"id"`
		Views     int64 `json:"page_views_count"`
		Reactions int64 `json:"public_reactions_count"`
		Comments  int64 `json:"comments_count"`
	}
	if err := do(ctx, request{method: "GET", url: devtoAPI + "/articles/me/published?per_page=1000", headers: d.headers()}, &articles); err != nil {
		return nil, err
	}
	for _, a := range articles {
		if strconv.FormatInt(a.ID, 10) == remoteID {
			return Metrics{"views": a.Views, "reactions": a.Reactions, "comments": a.Comments}, nil
		}
	}
	return nil, errors.New("article not found among your published articles")
}
