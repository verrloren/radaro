package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
)

func (a *app) opportunitiesCmd() *cobra.Command {
	var (
		days, limit int
		source      string
		project     int64
	)
	cmd := &cobra.Command{
		Use:   "opportunities [keyword]",
		Short: "Recent mentions worth answering that have no draft yet",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			q := url.Values{"days": {strconv.Itoa(days)}, "limit": {strconv.Itoa(limit)}}
			if len(args) == 1 {
				q.Set("q", strings.TrimSpace(args[0]))
			}
			if source != "" {
				q.Set("source", source)
			}
			if project != 0 {
				q.Set("p", strconv.FormatInt(project, 10))
			}
			var out []*model.Mention
			if err := c.Do(ctx, "GET", "/api/opportunities", q, nil, &out); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(out)
			}
			if len(out) == 0 {
				fmt.Println("No open opportunities. Scan first: radaro track \"keyword\"")
				return nil
			}
			for _, m := range out {
				fmt.Printf("%s  %-13s %-9s %s\n", m.ID, m.Source, sentimentOf(m), ago(m.CreatedAt))
				fmt.Printf("    %s\n    %s\n", clip(m.Content(), 140), deref(m.URL))
			}
			return nil
		}),
	}
	cmd.Flags().IntVar(&days, "days", 14, "only mentions from the last N days")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results")
	cmd.Flags().StringVar(&source, "source", "", "only this source (e.g. reddit)")
	cmd.Flags().Int64Var(&project, "project", 0, "only this project's keywords")
	return cmd
}

// newDraft is the body of POST /api/drafts.
type newDraft struct {
	ProjectID int64  `json:"project_id,omitempty"`
	Platform  string `json:"platform"`
	AccountID int64  `json:"account_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Community string `json:"community,omitempty"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body"`
	ReplyTo   string `json:"reply_to,omitempty"`
	Query     string `json:"query,omitempty"`
	MentionID string `json:"mention_id,omitempty"`
}

func (a *app) draftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Write, review and approve posts before anything is published",
	}

	var n newDraft
	var bodyFile string
	add := &cobra.Command{
		Use: "add", Short: "Create a draft (a new post, or a reply with --reply-to / --mention)", Args: cobra.NoArgs,
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
			if n.Platform == "" {
				return errors.New("--platform is required (reddit, bluesky, mastodon, devto)")
			}
			if _, ok := publish.LookupPlatform(n.Platform); !ok {
				return fmt.Errorf("unknown platform %q", n.Platform)
			}
			if bodyFile != "" {
				body, err := readBody(bodyFile)
				if err != nil {
					return err
				}
				n.Body = body
			}
			var d store.Draft
			if err := c.Do(ctx, "POST", "/api/drafts", nil, n, &d); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ draft %d created (%s). Review with: radaro draft show %d\n", d.ID, server.DraftSummary(&d), d.ID)
			return nil
		}),
	}
	add.Flags().StringVar(&n.Platform, "platform", "", "reddit | bluesky | mastodon | devto")
	add.Flags().StringVar(&n.Kind, "kind", "", "post | reply (default: reply when --reply-to is set)")
	add.Flags().StringVar(&n.Community, "community", "", "subreddit for Reddit, comma-separated tags for Dev.to")
	add.Flags().StringVar(&n.Title, "title", "", "title (Reddit and Dev.to posts)")
	add.Flags().StringVar(&n.Body, "body", "", "text of the post")
	add.Flags().StringVar(&bodyFile, "body-file", "", "read the text from a file, or - for stdin")
	add.Flags().StringVar(&n.ReplyTo, "reply-to", "", "link to the post or comment to answer")
	add.Flags().StringVar(&n.MentionID, "mention", "", "the opportunity (mention id) this answers")
	add.Flags().Int64Var(&n.ProjectID, "project", 0, "project the draft belongs to (default: your Default project)")
	add.Flags().Int64Var(&n.AccountID, "account", 0, "account id (default: picked from the project's pool at publish time, within limits)")
	add.Flags().StringVar(&n.Query, "query", "", "keyword this draft promotes")

	var status string
	var limit int
	list := &cobra.Command{
		Use: "list", Short: "List drafts, newest first", Args: cobra.NoArgs,
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
			q := url.Values{"limit": {strconv.Itoa(limit)}}
			if status != "" {
				q.Set("status", status)
			}
			var ds []store.Draft
			if err := c.Do(ctx, "GET", "/api/drafts", q, nil, &ds); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(ds)
			}
			if len(ds) == 0 {
				fmt.Println("No drafts.")
				return nil
			}
			fmt.Printf("%4s  %-10s %-9s %-6s %s\n", "ID", "STATUS", "PLATFORM", "KIND", "TEXT")
			for _, d := range ds {
				text := d.Body
				if d.Title != nil {
					text = *d.Title + " — " + text
				}
				fmt.Printf("%4d  %-10s %-9s %-6s %s\n", d.ID, d.Status, d.Platform, d.Kind, clip(text, 70))
			}
			return nil
		}),
	}
	list.Flags().StringVar(&status, "status", "", "draft | approved | published | failed | skipped | publishing")
	list.Flags().IntVar(&limit, "limit", 50, "maximum results")

	show := &cobra.Command{
		Use: "show <id>", Short: "Show a draft in full, with the account it would publish with and when", Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			d, raw, err := loadDraft(ctx, c, args[0])
			if err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(raw)
			}
			printDraft(d)
			printPlan(d, raw)
			return nil
		}),
	}

	var edit draftEdit
	var editCmd *cobra.Command
	editCmd = &cobra.Command{
		Use: "edit <id>", Short: "Change a draft's text or account (it goes back to review)", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			e, err := edit.patch(editCmd.Flags().Changed)
			if err != nil {
				return err
			}
			var updated store.Draft
			raw, err := call(ctx, c, http.MethodPatch, draftPath(d.ID, ""), e, &updated)
			if err != nil {
				return err
			}
			if err := server.ValidateDraft(&updated); err != nil {
				return fmt.Errorf("saved, but the draft is not publishable yet: %w", err)
			}
			if a.jsonFlag {
				return printJSON(raw)
			}
			fmt.Printf("✓ draft %d updated; approve it again when ready\n", d.ID)
			return nil
		}),
	}
	editCmd.Flags().StringVar(&edit.title, "title", "", "new title")
	editCmd.Flags().StringVar(&edit.body, "body", "", "new text")
	editCmd.Flags().StringVar(&edit.bodyFile, "body-file", "", "read the new text from a file, or - for stdin")
	editCmd.Flags().StringVar(&edit.community, "community", "", "new subreddit / tags")
	editCmd.Flags().Int64Var(&edit.account, "account", 0, "publish with this account id (0 = pick one at publish time)")

	approve := &cobra.Command{
		Use: "approve <id>", Short: "Approve a draft for publishing (the human-in-the-loop step)", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			if err := c.Do(ctx, "POST", draftPath(d.ID, "/approve"), nil, nil, d); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ draft %d approved. Publish with: radaro publish %d\n", d.ID, d.ID)
			return nil
		}),
	}

	skip := &cobra.Command{
		Use: "skip <id>", Short: "Discard a draft", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			if err := c.Do(ctx, "POST", draftPath(d.ID, "/skip"), nil, nil, d); err != nil {
				return err
			}
			fmt.Printf("✓ draft %d skipped\n", d.ID)
			return nil
		}),
	}

	cmd.AddCommand(add, list, show, editCmd, approve, skip)
	return cmd
}

func (a *app) publishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <draft-id>",
		Short: "Publish an approved draft within its account's limits",
		Long: "Publish an approved draft. A draft without an account gets one picked from the project's pool\n" +
			"(or any of your accounts on the platform): live or unchecked, not paused, not rate-limited,\n" +
			"within its limits, never a second account in the same thread; the most quota left first.\n" +
			"A refused publish sends nothing and says when the draft may go out; with --json it prints\n" +
			"{draft, account, error, next_at} and exits with status 1.",
		Args: cobra.ExactArgs(1),
		RunE: a.withDraft(a.publishDraft),
	}
}

// publishAnswer is the server's answer to a publish, successful or not.
type publishAnswer struct {
	Draft   json.RawMessage `json:"draft"`
	Account json.RawMessage `json:"account"`
	NextAt  *time.Time      `json:"next_at"`
}

func (a *app) publishDraft(ctx context.Context, c *client.Client, d *store.Draft) error {
	if d.Status != store.DraftApproved {
		return fmt.Errorf("draft %d is not approved (status: %s); approve it first: radaro draft approve %d", d.ID, d.Status, d.ID)
	}
	var res publishAnswer
	raw, err := call(ctx, c, http.MethodPost, draftPath(d.ID, "/publish"), nil, &res)
	if err != nil {
		return a.publishFailed(d, err)
	}
	if len(res.Draft) == 0 {
		res.Draft = raw // a server that answers with the bare draft
	}
	var pub store.Draft
	if err := json.Unmarshal(res.Draft, &pub); err != nil {
		return fmt.Errorf("unexpected answer to publish: %w", err)
	}
	if a.jsonFlag {
		return printJSON(res.Draft)
	}
	as := ""
	var acc store.Account
	if json.Unmarshal(res.Account, &acc) == nil && acc.Handle != "" {
		as = " as " + acc.Handle
	}
	fmt.Printf("✓ published draft %d%s: %s\n", pub.ID, as, deref(pub.RemoteURL))
	return nil
}

// publishFailed explains a refused or failed publish: when it may go out, and
// with --json the whole answer for an agent to act on.
func (a *app) publishFailed(d *store.Draft, err error) error {
	var ce *client.Error
	if !errors.As(err, &ce) {
		return err
	}
	var res publishAnswer
	_ = json.Unmarshal(ce.Body, &res) // not JSON: the message says it all
	if !a.jsonFlag {
		if res.NextAt != nil {
			return fmt.Errorf("%s; it may go out after %s (see radaro accounts)", ce.Message, when(*res.NextAt))
		}
		return ce
	}
	var draft any = d
	if len(res.Draft) > 0 && string(res.Draft) != "null" {
		draft = res.Draft
	}
	out := map[string]any{"draft": draft, "account": res.Account, "error": ce.Message, "next_at": res.NextAt}
	if err := printJSON(out); err != nil {
		return err
	}
	return exitError{1}
}

func (a *app) statsCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Engagement of published drafts (refreshed from each platform)",
		Args:  cobra.NoArgs,
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
			var res struct {
				Published []store.Draft `json:"published"`
				Errors    []string      `json:"errors"`
			}
			if err := c.Do(ctx, "GET", "/api/stats", url.Values{"refresh": {strconv.FormatBool(refresh)}}, nil, &res); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(res)
			}
			for _, p := range res.Errors {
				fmt.Printf("  ! %s\n", p)
			}
			if len(res.Published) == 0 {
				fmt.Println("Nothing published yet.")
				return nil
			}
			for _, d := range res.Published {
				removed := ""
				if d.RemovedAt != nil {
					removed = "  [removed]"
				}
				fmt.Printf("%4d  %-9s %s%s\n      %s  %s\n", d.ID, d.Platform, deref(d.RemoteURL), removed, metricsLine(d.Metrics), clip(d.Body, 60))
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&refresh, "refresh", true, "fetch fresh numbers from the platforms")
	return cmd
}

func (a *app) activityCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use: "activity", Short: "What was drafted, approved and published, newest first", Args: cobra.NoArgs,
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
			var acts []store.Activity
			if err := c.Do(ctx, "GET", "/api/activity", url.Values{"limit": {strconv.Itoa(limit)}}, nil, &acts); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(acts)
			}
			for _, act := range acts {
				draft := ""
				if act.DraftID != nil {
					draft = fmt.Sprintf("#%d ", *act.DraftID)
				}
				fmt.Printf("%s  %-18s %s%s\n", act.At[:19], act.Action, draft, clip(act.Detail, 90))
			}
			return nil
		}),
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum entries")
	return cmd
}

// --- helpers ------------------------------------------------------------------

func draftPath(id int64, suffix string) string {
	return "/api/drafts/" + strconv.FormatInt(id, 10) + suffix
}

// withDraft loads the draft named by the first argument.
func (a *app) withDraft(fn func(context.Context, *client.Client, *store.Draft) error) func(*cobra.Command, []string) error {
	return a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
		d, _, err := loadDraft(ctx, c, args[0])
		if err != nil {
			return err
		}
		return fn(ctx, c, d)
	})
}

// loadDraft is GET /api/drafts/{id}: the draft, and the answer as sent (with
// its publishing plan).
func loadDraft(ctx context.Context, c *client.Client, arg string) (*store.Draft, json.RawMessage, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id < 1 {
		return nil, nil, errors.New("draft id must be a positive number")
	}
	var d store.Draft
	raw, err := call(ctx, c, http.MethodGet, draftPath(id, ""), nil, &d)
	if client.StatusOf(err) == http.StatusNotFound {
		return nil, nil, fmt.Errorf("draft %d does not exist", id)
	}
	return &d, raw, err
}

// draftEdit holds the flags of `draft edit`; only the flags given change.
type draftEdit struct {
	title, body, bodyFile, community string
	account                          int64
}

func (e *draftEdit) patch(changed func(string) bool) (map[string]any, error) {
	p := map[string]any{}
	for name, v := range map[string]string{"title": e.title, "body": e.body, "community": e.community} {
		if changed(name) {
			p[name] = v
		}
	}
	if changed("body-file") {
		body, err := readBody(e.bodyFile)
		if err != nil {
			return nil, err
		}
		p["body"] = body
	}
	if changed("account") {
		p["account_id"] = accountOrAuto(e.account)
	}
	return p, nil
}

// accountOrAuto is the account_id of a draft edit: nil lets publishing pick one.
func accountOrAuto(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

// draftPlan is the publishing plan the server adds to a draft.
type draftPlan struct {
	Account *accountView `json:"account"`
	NextAt  *time.Time   `json:"next_at"`
	Reason  string       `json:"reason"`
}

func printPlan(d *store.Draft, raw json.RawMessage) {
	var res struct {
		Plan *draftPlan `json:"plan"`
	}
	if json.Unmarshal(raw, &res) != nil || res.Plan == nil {
		return
	}
	p := res.Plan
	acc := "none usable"
	if p.Account != nil {
		acc = fmt.Sprintf("#%d %s", p.Account.ID, p.Account.Handle)
	}
	if d.AccountID == nil {
		acc += " (picked at publish time)"
	}
	fmt.Printf("Account:   %s\n", acc)
	fmt.Printf("Next:      %s\n", planNext(p))
}

// planNext is when a draft may go out, and why not now.
func planNext(p *draftPlan) string {
	switch {
	case p.NextAt != nil && p.Reason != "":
		return when(*p.NextAt) + " — " + p.Reason
	case p.NextAt != nil:
		return when(*p.NextAt)
	case p.Reason != "":
		return "blocked — " + p.Reason
	}
	return "now"
}

func readBody(path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	return strings.TrimRight(string(b), "\n"), err
}

func printDraft(d *store.Draft) {
	fmt.Printf("Draft %d · %s · %s · %s\n", d.ID, d.Platform, d.Kind, d.Status)
	if d.Community != nil {
		fmt.Printf("Community: %s\n", *d.Community)
	}
	if d.ReplyTo != nil {
		fmt.Printf("Reply to:  %s\n", *d.ReplyTo)
	}
	if d.Title != nil {
		fmt.Printf("Title:     %s\n", *d.Title)
	}
	fmt.Printf("\n%s\n\n", d.Body)
	if d.RemoteURL != nil {
		fmt.Printf("Published: %s\n", *d.RemoteURL)
	}
	if d.Error != nil {
		fmt.Printf("Error:     %s\n", *d.Error)
	}
	if len(d.Metrics) > 0 {
		fmt.Printf("Metrics:   %s\n", metricsLine(d.Metrics))
	}
}

func metricsLine(m map[string]any) string {
	if len(m) == 0 {
		return "no metrics yet"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %v", k, m[k])
	}
	return strings.Join(parts, " · ")
}

func sentimentOf(m *model.Mention) string {
	if m.Sentiment == "" {
		return "unscored"
	}
	return string(m.Sentiment)
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
