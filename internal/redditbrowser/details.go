package redditbrowser

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Comment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
	Score  *int64 `json:"score"`
	URL    string `json:"url"`
}

type PostDetails struct {
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Author      string     `json:"author"`
	Community   string     `json:"community"`
	CreatedAt   *time.Time `json:"created_at"`
	Score       *int64     `json:"score"`
	Comments    *int64     `json:"comments"`
	UpvoteRatio *float64   `json:"upvote_ratio"`
	Flair       string     `json:"flair"`
	Locked      bool       `json:"locked"`
	Archived    bool       `json:"archived"`
	Removed     bool       `json:"removed"`
	NSFW        bool       `json:"nsfw"`
	CanReply    bool       `json:"can_reply"`
	Replies     []Comment  `json:"replies"`
	Rules       []Rule     `json:"rules"`
	FetchedAt   time.Time  `json:"fetched_at"`
}

func Details(ctx context.Context, c Credentials, target string) (PostDetails, error) {
	var out PostDetails
	err := WithAccount(ctx, c, func(s *Session) error {
		u, err := targetURL(target)
		if err != nil {
			return err
		}
		if err = s.Navigate(ctx, u); err != nil {
			return err
		}
		js := `(()=>{const n=document.querySelector('#siteTable > .thing.link')||document.querySelector('.thing.link');if(!n)return null;
		const text=(node,sel)=>node.querySelector(sel)?.textContent?.trim()||'';
		const number=v=>{const m=v.replace(/,/g,'').match(/-?\d+/);return m?Number(m[0]):null;};
		const score=node=>number(node.querySelector('.score.unvoted,.score')?.getAttribute('title')||text(node,'.score.unvoted,.score'));
		const comments=text(n,'a.comments');const count=number(comments)??(/^comment$/i.test(comments)?0:null);
		const body=text(n,'.usertext-body');const info=text(document,'.linkinfo .score');const ratio=info.match(/(\d+)%/);
		const locked=n.classList.contains('locked')||!!document.querySelector('.locked-notice');
		const archived=n.classList.contains('archived')||!!document.querySelector('.archived-notice');
		const replies=Array.from(document.querySelectorAll('.thing.comment')).slice(0,12).map(c=>{const entry=c.querySelector(':scope > .entry')||c;return {author:text(entry,'.author'),body:text(entry,'.usertext-body').slice(0,4000),score:score(entry),url:entry.querySelector('.bylink')?.href||''};});
		return {url:location.href,title:text(n,'a.title'),body:body.slice(0,40000),author:text(n,'.author'),community:text(document,'#header .redditname a')||text(n,'.subreddit'),created_at:n.querySelector('time')?.dateTime||null,score:score(n),comments:count,upvote_ratio:ratio?Number(ratio[1])/100:null,flair:text(n,'.linkflairlabel'),locked,archived,removed:body==='[removed]'||body==='[deleted]',nsfw:n.classList.contains('over18'),can_reply:!locked&&!archived&&!!document.querySelector('.commentarea > .usertext textarea[name="text"]'),replies,rules:[]};})()`
		if err = s.run(ctx, chromedp.Evaluate(js, &out)); err != nil {
			return err
		}
		if out.URL == "" {
			return errors.New("Reddit post details are unavailable; the page requires confirmation or has changed")
		}
		out.URL = canonical(out.URL)
		for i := range out.Replies {
			out.Replies[i].URL = canonical(out.Replies[i].URL)
		}
		out.Community = strings.TrimPrefix(strings.TrimSpace(out.Community), "r/")
		if communityPattern.MatchString(out.Community) {
			if err = s.Navigate(ctx, site+"/r/"+out.Community+"/about/rules"); err != nil {
				return err
			}
			if err = s.run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('.subreddit-rule-item'),n=>({name:n.querySelector('.subreddit-rule-title')?.textContent?.trim()||'',description:n.querySelector('.subreddit-rule-description')?.textContent?.trim().slice(0,4000)||''})).slice(0,30)`, &out.Rules)); err != nil {
				return err
			}
		}
		out.FetchedAt = time.Now().UTC()
		return nil
	})
	return out, err
}
