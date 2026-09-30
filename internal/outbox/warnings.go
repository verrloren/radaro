package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/store"
)

// Warning is advice shown before a draft is approved or published. Warnings
// never block publishing; limits do.
type Warning struct {
	Code string `json:"code"` // self_promo | duplicate | subreddit_rules | community_bans_promo | …
	Text string `json:"text"`
}

// Warning codes.
const (
	WarnSelfPromo          = "self_promo"
	WarnDuplicate          = "duplicate"
	WarnSubredditRules     = "subreddit_rules"
	WarnCommunityBansPromo = "community_bans_promo"
	WarnRulesUnavailable   = "subreddit_rules_unavailable"
)

const (
	warnWindow = 30 * 24 * time.Hour
	// SelfPromoLimit is the share of an account's publications that may point
	// at the project before Warnings says so.
	SelfPromoLimit = 0.25
	// duplicateSimilarity is the word-shingle Jaccard similarity from which two
	// texts count as near duplicates.
	duplicateSimilarity  = 0.5
	maxDuplicateWarnings = 3
	rulesTimeout         = 8 * time.Second
	rulesTTL             = time.Hour
)

// Warnings reviews one of the user's drafts: the account's self-promotion
// share, near duplicates published from the owner's other accounts, and the
// subreddit's rules. Only the owner's own publications and accounts are
// looked at; another user's are not "our" accounts.
func (s *Service) Warnings(ctx context.Context, userID int64, d *store.Draft) ([]Warning, error) {
	owner := ownerOf(userID, d)
	out := []Warning{}
	acc, err := s.warningAccount(owner, d)
	if err != nil {
		return nil, err
	}
	published, err := s.Store.PublishedDrafts(owner, d.Platform, s.now().Add(-warnWindow))
	if err != nil {
		return nil, err
	}
	handles, err := s.warnHandles(owner, d.Platform)
	if err != nil {
		return nil, err
	}
	if w, ok := selfPromo(d, acc, published); ok {
		out = append(out, w)
	}
	out = append(out, duplicates(d, acc, published, handles)...)
	if d.Platform == "reddit" {
		if sub := subredditOf(d); sub != "" {
			out = append(out, s.subredditWarnings(ctx, owner, acc, sub)...)
		}
	}
	return out, nil
}

// warningAccount is the account the draft would go out from: its own, or the
// one the outbox would pick now. nil when that is not known yet.
func (s *Service) warningAccount(owner int64, d *store.Draft) (*store.Account, error) {
	if d.AccountID != nil {
		return s.Store.Account(owner, *d.AccountID)
	}
	if acc, err := s.PickAccount(owner, d); err == nil {
		return acc, nil
	}
	return nil, nil
}

func (s *Service) warnHandles(owner int64, platform string) (map[int64]string, error) {
	accs, err := s.Store.Accounts(owner, platform)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(accs))
	for _, a := range accs {
		out[a.ID] = a.Handle
	}
	return out, nil
}

// selfPromo flags an account whose publications mostly point at the project.
// Radaro cannot know the project's own URLs, so the draft stands in for it: a
// publication promotes the project when it names the draft's keyword (query)
// as a whole word, or links to one of the links in the draft's text (the same
// page or a page under it). Only what Radaro published is counted; activity
// outside Radaro is invisible here.
func selfPromo(d *store.Draft, acc *store.Account, published []*store.Draft) (Warning, bool) {
	if acc == nil {
		return Warning{}, false
	}
	m := newPromoMatcher(d)
	if !m.matches(d) {
		return Warning{}, false
	}
	total, promo := 1, 1 // this draft
	for _, p := range published {
		if p.ID == d.ID || p.AccountID == nil || *p.AccountID != acc.ID {
			continue
		}
		total++
		if m.matches(p) {
			promo++
		}
	}
	share := float64(promo) / float64(total)
	if share <= SelfPromoLimit {
		return Warning{}, false
	}
	return Warning{Code: WarnSelfPromo, Text: fmt.Sprintf(
		"%d of %d publications from %s in the last 30 days, this one included, name or link the project (%.0f%%). "+
			"Accounts that mostly promote one thing get flagged as spam; mix in answers that don't mention it.",
		promo, total, acc.Handle, share*100)}, true
}

type promoMatcher struct {
	query *regexp.Regexp
	links []string
}

func newPromoMatcher(d *store.Draft) promoMatcher {
	var m promoMatcher
	if d.Query != nil {
		if q := strings.TrimSpace(*d.Query); q != "" {
			m.query = wordPattern(q)
		}
	}
	m.links = pageLinks(warnText(d))
	return m
}

func (m promoMatcher) matches(d *store.Draft) bool {
	text := warnText(d)
	if m.query != nil && m.query.MatchString(text) {
		return true
	}
	for _, l := range pageLinks(text) {
		for _, own := range m.links {
			if l == own || strings.HasPrefix(l, own+"/") || strings.HasPrefix(own, l+"/") {
				return true
			}
		}
	}
	return false
}

// wordPattern matches q case-insensitively, not inside a longer word.
func wordPattern(q string) *regexp.Regexp {
	p := regexp.QuoteMeta(q)
	r := []rune(q)
	if isWordRune(r[0]) {
		p = `\b` + p
	}
	if isWordRune(r[len(r)-1]) {
		p += `\b`
	}
	return regexp.MustCompile(`(?i)` + p)
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func warnText(d *store.Draft) string {
	if d.Title != nil {
		return *d.Title + "\n" + d.Body
	}
	return d.Body
}

var linkRe = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// links returns the text's URLs as host/path: no scheme, www, query or
// fragment, so the same page matches however it was written.
func pageLinks(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range linkRe.FindAllString(text, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?*_~"))
		if err != nil || u.Host == "" {
			continue
		}
		l := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") + strings.TrimRight(u.EscapedPath(), "/")
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// duplicates finds publications on the same platform from other accounts
// with nearly the same text or a link in common: several accounts pushing the
// same thing is what platforms treat as coordinated spam. Cross-posting to
// other platforms is normal and not flagged.
func duplicates(d *store.Draft, acc *store.Account, published []*store.Draft, handles map[int64]string) []Warning {
	out := []Warning{}
	mine := shingles(warnText(d))
	myLinks := pageLinks(warnText(d))
	for _, p := range published {
		if len(out) == maxDuplicateWarnings {
			break
		}
		if p.ID == d.ID || p.AccountID == nil || (acc != nil && *p.AccountID == acc.ID) {
			continue
		}
		who := handles[*p.AccountID]
		if who == "" {
			who = fmt.Sprintf("account %d", *p.AccountID)
		}
		where := "draft " + fmt.Sprint(p.ID)
		if p.RemoteURL != nil {
			where = *p.RemoteURL
		}
		if sim := jaccard(mine, shingles(warnText(p))); sim >= duplicateSimilarity {
			out = append(out, Warning{Code: WarnDuplicate, Text: fmt.Sprintf(
				"Nearly the same text (%.0f%% similar) was published from %s: %s", sim*100, who, where)})
			continue
		}
		if l := commonLink(myLinks, pageLinks(warnText(p))); l != "" {
			out = append(out, Warning{Code: WarnDuplicate, Text: fmt.Sprintf(
				"The link %s was already published from %s: %s", l, who, where)})
		}
	}
	return out
}

func commonLink(a, b []string) string {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return x
			}
		}
	}
	return ""
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// shingles are the text's overlapping three-word sequences; a short text is one shingle.
func shingles(text string) map[string]bool {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	out := map[string]bool{}
	if len(words) < 3 {
		if len(words) > 0 {
			out[strings.Join(words, " ")] = true
		}
		return out
	}
	for i := 0; i+3 <= len(words); i++ {
		out[strings.Join(words[i:i+3], " ")] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

var threadSubRe = regexp.MustCompile(`(?i)reddit\.com/r/([A-Za-z0-9_]+)`)

// subredditOf is the draft's subreddit: its community, or the one of the thread it answers.
func subredditOf(d *store.Draft) string {
	if d.Community != nil {
		c := strings.TrimSpace(*d.Community)
		c = strings.TrimPrefix(strings.TrimPrefix(c, "/"), "r/")
		if c != "" {
			return c
		}
	}
	if d.ReplyTo != nil {
		if m := threadSubRe.FindStringSubmatch(*d.ReplyTo); m != nil {
			return m[1]
		}
	}
	return ""
}

// promoRuleRe spots rules that restrict self-promotion or advertising.
var promoRuleRe = regexp.MustCompile(`(?i)self[- ]?promo|promot|advertis|\bads?\b|spam|affiliate|marketing|shill|referral|\b10\s?%|9:1|own (content|project|product|work|blog|app)|no links`)

func (s *Service) subredditWarnings(ctx context.Context, owner int64, acc *store.Account, sub string) []Warning {
	rules, err := s.subredditRules(ctx, owner, acc, sub)
	if err != nil {
		return []Warning{{Code: WarnRulesUnavailable, Text: fmt.Sprintf(
			"Could not load the rules of r/%s; read them on Reddit before publishing.", sub)}}
	}
	out := []Warning{}
	for _, r := range rules {
		text := strings.TrimSpace(r.Name)
		if desc := strings.Join(strings.Fields(r.Description), " "); desc != "" {
			text += " — " + desc
		}
		if promoRuleRe.MatchString(r.Name + " " + r.Description) {
			out = append(out, Warning{Code: WarnCommunityBansPromo, Text: fmt.Sprintf(
				"r/%s restricts self-promotion: %s", sub, text)})
			continue
		}
		out = append(out, Warning{Code: WarnSubredditRules, Text: fmt.Sprintf("r/%s rule: %s", sub, text)})
	}
	return out
}

type rulesReader interface {
	SubredditRules(ctx context.Context, subreddit string) ([]publish.SubredditRule, error)
}

type cachedRules struct {
	rules []publish.SubredditRule
	at    time.Time
}

// rulesCache keeps subreddit rules for rulesTTL: they rarely change, and the
// dashboard asks for warnings every time a draft is opened.
var rulesCache = struct {
	sync.Mutex
	m map[string]cachedRules
}{m: map[string]cachedRules{}}

func (s *Service) subredditRules(ctx context.Context, owner int64, acc *store.Account, sub string) ([]publish.SubredditRule, error) {
	key := strings.ToLower(sub)
	now := s.now()
	rulesCache.Lock()
	c, ok := rulesCache.m[key]
	rulesCache.Unlock()
	if ok && now.Sub(c.at) < rulesTTL {
		return c.rules, nil
	}

	creds, err := s.redditCredentials(owner, acc)
	if err != nil {
		return nil, err
	}
	newPub := s.NewPublisher
	if newPub == nil {
		newPub = publish.New
	}
	pub, err := newPub("reddit", creds, s.Version)
	if err != nil {
		return nil, err
	}
	rr, ok := pub.(rulesReader)
	if !ok {
		return nil, fmt.Errorf("the reddit connector cannot read rules")
	}
	ctx, cancel := context.WithTimeout(ctx, rulesTimeout)
	defer cancel()
	rules, err := rr.SubredditRules(ctx, sub)
	if err != nil {
		return nil, err
	}
	rulesCache.Lock()
	rulesCache.m[key] = cachedRules{rules: rules, at: now}
	rulesCache.Unlock()
	return rules, nil
}

// redditCredentials reads rules as the draft's account, else any usable
// Reddit account of the owner (never another user's): the rules endpoint
// needs an OAuth token.
func (s *Service) redditCredentials(owner int64, acc *store.Account) (json.RawMessage, error) {
	if acc != nil && acc.Platform == "reddit" {
		return acc.Credentials, nil
	}
	accs, err := s.Store.Accounts(owner, "reddit")
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		if a.Status != store.AccountInvalid && a.Status != store.AccountSuspended {
			return a.Credentials, nil
		}
	}
	return nil, errors.New("no reddit account connected")
}
