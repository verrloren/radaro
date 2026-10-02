package redditbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

var communityPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var fullnamePattern = regexp.MustCompile(`^t[13]_[a-z0-9]+$`)
var commentURLPattern = regexp.MustCompile(`/comments/[a-z0-9]+/[^/]*/([a-z0-9]+)`)

func commentID(target string) string {
	if strings.HasPrefix(target, "t1_") && fullnamePattern.MatchString(target) {
		return strings.TrimPrefix(target, "t1_")
	}
	if match := commentURLPattern.FindStringSubmatch(target); len(match) > 1 {
		return match[1]
	}
	return ""
}

// Only trusted Reddit pages are used as navigation targets, never an arbitrary
// URL from a scanned mention or draft.
func targetURL(raw string) (string, error) {
	if fullnamePattern.MatchString(raw) {
		return site + "/by_id/" + raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || u.Hostname() != "reddit.com" && !strings.HasSuffix(u.Hostname(), ".reddit.com") || u.Port() != "" || !strings.Contains(u.Path, "/comments/") {
		return "", errors.New("a Reddit post or comment URL is required")
	}
	base, _ := url.Parse(site)
	u.Scheme, u.Host = base.Scheme, base.Host
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func canonical(raw string) string {
	u, err := url.Parse(raw)
	base, _ := url.Parse(site)
	if err != nil || raw == "" || u.User != nil || u.Host != "" && u.Host != base.Host && u.Hostname() != "reddit.com" && !strings.HasSuffix(u.Hostname(), ".reddit.com") {
		return ""
	}
	u.Scheme, u.Host = "https", "www.reddit.com"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

type Post struct{ Kind, Community, Title, Body, ReplyTo string }
type Result struct {
	RemoteID string
	URL      string
}
type UncertainError struct{}

func (*UncertainError) Error() string {
	return "Reddit submission may have succeeded; check Reddit before retrying this draft"
}
func (*UncertainError) PublishUncertain() bool { return true }

func Publish(ctx context.Context, c Credentials, p Post) (Result, error) {
	var out Result
	err := WithAccount(ctx, c, func(s *Session) error {
		if p.Kind == "reply" {
			return s.reply(ctx, c.Username, p, &out)
		}
		sub := strings.Trim(strings.TrimPrefix(strings.TrimSpace(p.Community), "r/"), "/")
		if !communityPattern.MatchString(sub) {
			return errors.New("invalid subreddit")
		}
		if err := s.Navigate(ctx, site+"/r/"+sub+"/submit?selftext=true"); err != nil {
			return err
		}
		if err := s.run(ctx, chromedp.WaitVisible(`input[name="title"],textarea[name="title"]`, chromedp.ByQuery), chromedp.SetValue(`[name="title"]`, p.Title, chromedp.ByQuery), chromedp.SetValue(`textarea[name="text"]`, p.Body, chromedp.ByQuery)); err != nil {
			return err
		}
		// Once Submit is dispatched a missing confirmation is ambiguous. The
		// outbox keeps such drafts in publishing instead of allowing a duplicate.
		if err := s.run(ctx, chromedp.Click(`button[name="submit"]`, chromedp.ByQuery)); err != nil {
			return &UncertainError{}
		}
		var confirmed bool
		if err := s.wait(ctx, `location.pathname.includes('/comments/') && !!document.querySelector('.thing.link[data-fullname]')`, &confirmed, 15*time.Second, false); err != nil {
			return &UncertainError{}
		}
		var loc string
		if err := s.run(ctx, chromedp.Location(&loc)); err != nil {
			return &UncertainError{}
		}
		out.URL = canonical(loc)
		out.RemoteID = out.URL
		return nil
	})
	return out, err
}

func (s *Session) reply(ctx context.Context, username string, p Post, out *Result) error {
	target, err := targetURL(p.ReplyTo)
	if err != nil {
		return err
	}
	if err = s.Navigate(ctx, target); err != nil {
		return err
	}
	// Select the reply form for the target comment, or the post's top-level form.
	comment := commentID(p.ReplyTo)
	form := `.commentarea > .usertext`
	if comment != "" {
		if err = s.run(ctx, chromedp.Click(`.thing.id-t1_`+comment+` > .entry .buttons .reply-button a`, chromedp.ByQuery)); err != nil {
			return err
		}
		form = `.thing.id-t1_` + comment + ` > .entry form.usertext:has(textarea)`
	}
	var before []string
	if err = s.run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('.thing.comment[data-fullname]'),n=>n.dataset.fullname)`, &before), chromedp.WaitVisible(form+` textarea[name="text"]`, chromedp.ByQuery), chromedp.SetValue(form+` textarea[name="text"]`, p.Body, chromedp.ByQuery)); err != nil {
		return err
	}
	if err = s.run(ctx, chromedp.Click(form+` button.save`, chromedp.ByQuery)); err != nil {
		return &UncertainError{}
	}
	args, _ := json.Marshal([]any{before, username})
	var link string
	js := `()=>{const [before,user]=` + string(args) + `;const n=Array.from(document.querySelectorAll('.thing.comment[data-fullname]')).find(n=>!before.includes(n.dataset.fullname)&&n.querySelector(':scope > .entry .author')?.textContent===user);return n?.querySelector(':scope > .entry .bylink')?.href||false;}`
	if err = s.wait(ctx, js, &link, 15*time.Second, true); err != nil {
		return &UncertainError{}
	}
	out.URL = canonical(link)
	out.RemoteID = out.URL
	return nil
}

type Mention struct {
	Community string    `json:"community"`
	Comments  *int64    `json:"comments"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	Score     *int64    `json:"score"`
}
type SearchPage struct {
	Mentions []Mention `json:"mentions"`
	Next     string    `json:"next"`
}

func Search(ctx context.Context, c Credentials, q, cursor string, limit int) (SearchPage, error) {
	if limit < 1 {
		return SearchPage{}, errors.New("Reddit search limit must be positive")
	}
	var out SearchPage
	err := WithAccount(ctx, c, func(s *Session) error {
		params := url.Values{"q": {q}, "sort": {"new"}, "type": {"link"}}
		if cursor != "" {
			if !fullnamePattern.MatchString(cursor) {
				return errors.New("invalid Reddit search cursor")
			}
			params.Set("after", cursor)
		}
		if err := s.Navigate(ctx, site+"/search?"+params.Encode()); err != nil {
			return err
		}
		js := `(()=>{const rows=Array.from(document.querySelectorAll('.search-result-link'));const text=(n,s)=>n.querySelector(s)?.textContent?.trim()||''; const mentions=rows.map(n=>({title:text(n,'.search-title'),body:text(n,'.search-result-body'),author:text(n,'.search-author'),url:(n.querySelector('.search-comments')||n.querySelector('.search-title'))?.href||'',created_at:n.querySelector('time')?.dateTime||null,score:(text(n,'.search-score').replace(/,/g,'').match(/-?\d+/)||[])[0],community:text(n,'.search-subreddit-link').replace(/^r\//,''),comments:(text(n,'.search-comments').replace(/,/g,'').match(/\d+/)||[])[0]}));let next='';const a=Array.from(document.querySelectorAll('.nav-buttons a,.nextprev a')).find(a=>/next/i.test(a.textContent));if(a)next=new URL(a.href).searchParams.get('after')||'';return {mentions:mentions.map(m=>({...m,score:m.score===undefined?null:Number(m.score),comments:m.comments===undefined?null:Number(m.comments)})),next};})()`
		if err := s.run(ctx, chromedp.Evaluate(js, &out)); err != nil {
			return err
		}
		var available bool
		if err := s.run(ctx, chromedp.Evaluate(`!!document.querySelector('.search-result-listing,.searchpane')`, &available)); err != nil {
			return err
		}
		if !available {
			return errors.New("Reddit search requires confirmation or its page has changed")
		}
		if len(out.Mentions) > limit {
			out.Mentions = out.Mentions[:limit]
		}
		valid := out.Mentions[:0]
		for _, m := range out.Mentions {
			m.URL = canonical(m.URL)
			if m.URL != "" {
				valid = append(valid, m)
			}
		}
		out.Mentions = valid
		return nil
	})
	return out, err
}

func Metrics(ctx context.Context, c Credentials, target string) (map[string]any, error) {
	out := map[string]any{}
	err := WithAccount(ctx, c, func(s *Session) error {
		u, err := targetURL(target)
		if err != nil {
			return err
		}
		if err = s.Navigate(ctx, u); err != nil {
			return err
		}
		selector := ".thing.link"
		if id := commentID(target); id != "" {
			selector = ".thing.id-t1_" + id + " > .entry"
		}
		encoded, _ := json.Marshal(selector)
		js := `(()=>{const n=document.querySelector(` + string(encoded) + `);if(!n)return {available:false};const score=n.querySelector('.score.unvoted,.score')?.getAttribute('title')||n.querySelector('.score')?.textContent||'';const comments=n.querySelector('a.comments')?.textContent||'';const body=n.querySelector('.usertext-body')?.textContent?.trim()||'';const result={available:true,removed:body==='[removed]'||body==='[deleted]'};const sc=score.match(/^-?\d+/);if(sc)result.score=Number(sc[0]);const count=comments.match(/\d+/);if(count)result.comments=Number(count[0]);return result;})()`
		if err = s.run(ctx, chromedp.Evaluate(js, &out)); err != nil {
			return err
		}
		if out["available"] != true {
			return errors.New("Reddit metrics are unavailable; the page requires confirmation or has changed")
		}
		delete(out, "available")
		return nil
	})
	return out, err
}

type Rule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func Rules(ctx context.Context, c Credentials, community string) ([]Rule, error) {
	var out []Rule
	if !communityPattern.MatchString(community) {
		return nil, errors.New("invalid subreddit")
	}
	err := WithAccount(ctx, c, func(s *Session) error {
		if err := s.Navigate(ctx, site+"/r/"+community+"/about/rules"); err != nil {
			return err
		}
		return s.run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('.subreddit-rule-item'),n=>({name:n.querySelector('.subreddit-rule-title')?.textContent?.trim()||'',description:n.querySelector('.subreddit-rule-description')?.textContent?.trim()||''}))`, &out))
	})
	return out, err
}
