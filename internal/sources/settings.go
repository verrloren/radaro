package sources

import "strings"

// Field is one credential or setting a keyed source needs. The dashboard
// renders a form from these; values are stored in the local database and
// override the RADARO_* environment field by field.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Multiline   bool   `json:"multiline,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Env         string `json:"env"`
}

var fields = map[string][]Field{
	"reddit": {
		{Key: "client_id", Label: "Client ID", Placeholder: "from reddit.com/prefs/apps", Env: "RADARO_REDDIT_CLIENT_ID"},
		{Key: "client_secret", Label: "Client secret", Secret: true, Env: "RADARO_REDDIT_CLIENT_SECRET"},
		{Key: "access_token", Label: "Access token (instead of an app)", Secret: true, Env: "RADARO_REDDIT_ACCESS_TOKEN"},
	},
	"mastodon": {
		{Key: "instance", Label: "Instance", Placeholder: "mastodon.social", Env: "RADARO_MASTODON_INSTANCE"},
		{Key: "access_token", Label: "Access token", Secret: true, Env: "RADARO_MASTODON_ACCESS_TOKEN"},
	},
	"rss": {
		{Key: "feeds", Label: "Feed URLs, one per line", Multiline: true, Placeholder: "https://example.com/feed.xml", Env: "RADARO_RSS_FEEDS"},
	},
	"x": {
		{Key: "bearer_token", Label: "Bearer token", Secret: true, Env: "RADARO_X_BEARER_TOKEN"},
	},
	"youtube": {
		{Key: "api_key", Label: "API key", Secret: true, Env: "RADARO_YOUTUBE_API_KEY"},
	},
}

// Fields lists the settings source name accepts (nil for zero-config sources).
func Fields(name string) []Field { return fields[name] }

// Value returns the current value of one setting ("" when unset).
func (o Options) Value(source, key string) string {
	switch source + "." + key {
	case "reddit.client_id":
		return o.RedditClientID
	case "reddit.client_secret":
		return o.RedditClientSecret
	case "reddit.access_token":
		return o.RedditAccessToken
	case "mastodon.instance":
		return o.MastodonInstance
	case "mastodon.access_token":
		return o.MastodonAccessToken
	case "rss.feeds":
		return strings.Join(o.RSSFeeds, "\n")
	case "x.bearer_token":
		return o.XBearerToken
	case "youtube.api_key":
		return o.YouTubeAPIKey
	}
	return ""
}

func (o *Options) set(source, key, v string) {
	switch source + "." + key {
	case "reddit.client_id":
		o.RedditClientID = v
	case "reddit.client_secret":
		o.RedditClientSecret = v
	case "reddit.access_token":
		o.RedditAccessToken = v
	case "mastodon.instance":
		o.MastodonInstance = v
	case "mastodon.access_token":
		o.MastodonAccessToken = v
	case "rss.feeds":
		o.RSSFeeds = SplitFeeds(v)
	case "x.bearer_token":
		o.XBearerToken = v
	case "youtube.api_key":
		o.YouTubeAPIKey = v
	}
}

// Merge returns o with the stored settings (source → key → value) laid over
// it; blank stored values leave the environment's value in place.
func (o Options) Merge(stored map[string]map[string]string) Options {
	o.RSSFeeds = append([]string(nil), o.RSSFeeds...)
	for source, values := range stored {
		for _, f := range fields[source] {
			if v := strings.TrimSpace(values[f.Key]); v != "" {
				o.set(source, f.Key, v)
			}
		}
	}
	return o
}

// SplitFeeds splits a feed list on newlines and commas.
func SplitFeeds(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
