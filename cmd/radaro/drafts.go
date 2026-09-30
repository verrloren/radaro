package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	add.Flags().Int64Var(&n.AccountID, "account", 0, "account id (default: the project's account for the platform)")
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
		Use: "show <id>", Short: "Show a draft in full", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			if a.jsonFlag {
				return printJSON(d)
			}
			printDraft(d)
			return nil
		}),
	}

	var edit struct {
		title, body, bodyFile, community string
	}
	var editCmd *cobra.Command
	editCmd = &cobra.Command{
		Use: "edit <id>", Short: "Change a draft's text (it goes back to review)", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			e := map[string]string{}
			flags := editCmd.Flags()
			if flags.Changed("title") {
				e["title"] = edit.title
			}
			if flags.Changed("body") {
				e["body"] = edit.body
			}
			if flags.Changed("body-file") {
				body, err := readBody(edit.bodyFile)
				if err != nil {
					return err
				}
				e["body"] = body
			}
			if flags.Changed("community") {
				e["community"] = edit.community
			}
			var updated store.Draft
			if err := c.Do(ctx, "PATCH", draftPath(d.ID, ""), nil, e, &updated); err != nil {
				return err
			}
			if err := server.ValidateDraft(&updated); err != nil {
				return fmt.Errorf("saved, but the draft is not publishable yet: %w", err)
			}
			if a.jsonFlag {
				return printJSON(updated)
			}
			fmt.Printf("✓ draft %d updated; approve it again when ready\n", d.ID)
			return nil
		}),
	}
	editCmd.Flags().StringVar(&edit.title, "title", "", "new title")
	editCmd.Flags().StringVar(&edit.body, "body", "", "new text")
	editCmd.Flags().StringVar(&edit.bodyFile, "body-file", "", "read the new text from a file, or - for stdin")
	editCmd.Flags().StringVar(&edit.community, "community", "", "new subreddit / tags")

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
		Short: "Publish an approved draft to its platform",
		Args:  cobra.ExactArgs(1),
		RunE: a.withDraft(func(ctx context.Context, c *client.Client, d *store.Draft) error {
			if d.Status != store.DraftApproved {
				return fmt.Errorf("draft %d is not approved (status: %s); approve it first: radaro draft approve %d", d.ID, d.Status, d.ID)
			}
			if err := c.Do(ctx, "POST", draftPath(d.ID, "/publish"), nil, nil, d); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ published draft %d: %s\n", d.ID, deref(d.RemoteURL))
			return nil
		}),
	}
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
				fmt.Printf("%4d  %-9s %s\n      %s  %s\n", d.ID, d.Platform, deref(d.RemoteURL), metricsLine(d.Metrics), clip(d.Body, 60))
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
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("draft id must be a positive number")
		}
		var d store.Draft
		if err := c.Do(ctx, "GET", draftPath(id, ""), nil, nil, &d); err != nil {
			if client.StatusOf(err) == 404 {
				return fmt.Errorf("draft %d does not exist", id)
			}
			return err
		}
		return fn(ctx, c, &d)
	})
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
