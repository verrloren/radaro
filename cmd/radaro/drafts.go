package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/publish"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
)

func (a *app) opportunitiesCmd() *cobra.Command {
	var (
		days   int
		limit  int
		source string
	)
	cmd := &cobra.Command{
		Use:   "opportunities [keyword]",
		Short: "Recent mentions worth answering that have no draft yet",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withStore(func(st *store.Store, args []string) error {
			sc := store.Scope{}
			if len(args) == 1 {
				sc.Query = strings.TrimSpace(args[0])
			}
			mentions, err := st.Mentions(store.MentionFilter{Scope: sc, Source: source})
			if err != nil {
				return err
			}
			drafted, err := st.DraftedMentionIDs(0)
			if err != nil {
				return err
			}
			cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
			out := []server.MentionView{}
			for _, m := range mentions {
				if m.CreatedAt.Before(cutoff) || drafted[m.ID] || m.URL == nil {
					continue
				}
				out = append(out, server.View(m))
				if len(out) == limit {
					break
				}
			}
			if a.jsonFlag {
				return printJSON(out)
			}
			if len(out) == 0 {
				fmt.Println("No open opportunities. Scan first: radaro track \"keyword\"")
				return nil
			}
			for _, v := range out {
				fmt.Printf("%s  %-13s %-9s %s\n", v.ID, v.Source, sentimentOf(v.Mention), ago(v.CreatedAt))
				fmt.Printf("    %s\n    %s\n", clip(v.Content(), 140), deref(v.URL))
			}
			return nil
		}),
	}
	cmd.Flags().IntVar(&days, "days", 14, "only mentions from the last N days")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results")
	cmd.Flags().StringVar(&source, "source", "", "only this source (e.g. reddit)")
	return cmd
}

func (a *app) draftCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Write, review and approve posts before anything is published",
	}

	var n store.NewDraft
	var bodyFile string
	add := &cobra.Command{
		Use: "add", Short: "Create a draft (a new post, or a reply with --reply-to / --mention)", Args: cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			if n.Platform == "" {
				return errors.New("--platform is required (reddit, bluesky, mastodon, devto)")
			}
			pl, ok := publish.LookupPlatform(n.Platform)
			if !ok {
				return fmt.Errorf("unknown platform %q", n.Platform)
			}
			if bodyFile != "" {
				body, err := readBody(bodyFile)
				if err != nil {
					return err
				}
				n.Body = body
			}
			if n.MentionID != "" && n.ReplyTo == "" {
				m, err := mentionByID(st, n.MentionID)
				if err != nil {
					return err
				}
				if m.Source == n.Platform && m.URL != nil {
					n.ReplyTo = *m.URL // answering the thread it came from
				}
				n.Query = m.Query
			}
			if n.Kind == "" {
				n.Kind = "post"
				if n.ReplyTo != "" {
					n.Kind = "reply"
				}
			}
			if err := pl.Validate(publish.Post{Kind: n.Kind, Community: n.Community, Title: n.Title, Body: n.Body, ReplyTo: n.ReplyTo}); err != nil {
				return err
			}
			if n.AccountID != 0 {
				if acc, err := st.Account(0, n.AccountID); err != nil {
					return err
				} else if acc == nil || acc.Platform != n.Platform {
					return fmt.Errorf("account %d is not a %s account", n.AccountID, n.Platform)
				}
			}
			d, err := st.CreateDraft(n)
			if err != nil {
				return err
			}
			_ = st.LogActivity(0, "draft.created", d.ID, draftSummary(d))
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ draft %d created (%s). Review with: radaro draft show %d\n", d.ID, draftSummary(d), d.ID)
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
	add.Flags().Int64Var(&n.AccountID, "account", 0, "account id (default: the only account for the platform)")
	add.Flags().StringVar(&n.Query, "query", "", "keyword this draft promotes")

	var status string
	var limit int
	list := &cobra.Command{
		Use: "list", Short: "List drafts, newest first", Args: cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			ds, err := st.Drafts(0, status, limit)
			if err != nil {
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
		RunE: a.withDraft(func(st *store.Store, d *store.Draft) error {
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
		RunE: a.withDraft(func(st *store.Store, d *store.Draft) error {
			var e store.DraftEdit
			flags := editCmd.Flags()
			if flags.Changed("title") {
				e.Title = &edit.title
			}
			if flags.Changed("body") {
				e.Body = &edit.body
			}
			if flags.Changed("body-file") {
				body, err := readBody(edit.bodyFile)
				if err != nil {
					return err
				}
				e.Body = &body
			}
			if flags.Changed("community") {
				e.Community = &edit.community
			}
			updated, err := st.EditDraft(0, d.ID, e)
			if err != nil {
				return err
			}
			if err := validateDraft(updated); err != nil {
				return fmt.Errorf("saved, but the draft is not publishable yet: %w", err)
			}
			_ = st.LogActivity(0, "draft.edited", d.ID, draftSummary(updated))
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
		RunE: a.withDraft(func(st *store.Store, d *store.Draft) error {
			if err := validateDraft(d); err != nil {
				return err
			}
			d, err := st.ApproveDraft(0, d.ID)
			if err != nil {
				return err
			}
			_ = st.LogActivity(0, "draft.approved", d.ID, draftSummary(d))
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ draft %d approved. Publish with: radaro publish %d\n", d.ID, d.ID)
			return nil
		}),
	}

	skip := &cobra.Command{
		Use: "skip <id>", Short: "Discard a draft", Args: cobra.ExactArgs(1),
		RunE: a.withDraft(func(st *store.Store, d *store.Draft) error {
			d, err := st.SkipDraft(0, d.ID)
			if err != nil {
				return err
			}
			_ = st.LogActivity(0, "draft.skipped", d.ID, draftSummary(d))
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
		RunE: a.withDraft(func(st *store.Store, d *store.Draft) error {
			if d.Status != store.DraftApproved {
				return fmt.Errorf("draft %d is not approved (status: %s); approve it first: radaro draft approve %d", d.ID, d.Status, d.ID)
			}
			if err := validateDraft(d); err != nil {
				return err
			}
			acc, err := accountFor(st, d)
			if err != nil {
				return err
			}
			pub, err := publish.New(acc.Platform, acc.Credentials, version)
			if err != nil {
				return err
			}
			if _, err := st.BeginPublish(0, d.ID); err != nil {
				return err
			}
			res, pubErr := pub.Publish(a.ctx(), postOf(d))
			d, err = st.FinishPublish(d.ID, res.RemoteID, res.URL, pubErr)
			if err != nil {
				return err
			}
			if pubErr != nil {
				_ = st.LogActivity(0, "draft.failed", d.ID, pubErr.Error())
				return fmt.Errorf("publishing draft %d failed: %w", d.ID, pubErr)
			}
			_ = st.LogActivity(0, "draft.published", d.ID, acc.Platform+" "+acc.Handle+" "+res.URL)
			if a.jsonFlag {
				return printJSON(d)
			}
			fmt.Printf("✓ published draft %d as %s: %s\n", d.ID, acc.Handle, res.URL)
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
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			ds, err := st.Drafts(0, store.DraftPublished, 0)
			if err != nil {
				return err
			}
			var problems []string
			if refresh {
				for _, d := range ds {
					if d.RemoteID == nil {
						continue
					}
					acc, err := accountFor(st, d)
					if err != nil {
						problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
						continue
					}
					pub, err := publish.New(acc.Platform, acc.Credentials, version)
					if err != nil {
						problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
						continue
					}
					m, err := pub.Metrics(a.ctx(), *d.RemoteID)
					if err != nil {
						problems = append(problems, fmt.Sprintf("draft %d: %v", d.ID, err))
						continue
					}
					if err := st.SaveMetrics(d.ID, m); err != nil {
						return err
					}
				}
				if ds, err = st.Drafts(0, store.DraftPublished, 0); err != nil {
					return err
				}
			}
			if a.jsonFlag {
				return printJSON(map[string]any{"published": ds, "errors": problems})
			}
			for _, p := range problems {
				fmt.Printf("  ! %s\n", p)
			}
			if len(ds) == 0 {
				fmt.Println("Nothing published yet.")
				return nil
			}
			for _, d := range ds {
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
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			acts, err := st.Activities(0, limit)
			if err != nil {
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

func (a *app) withDraft(fn func(*store.Store, *store.Draft) error) func(*cobra.Command, []string) error {
	return a.withStore(func(st *store.Store, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("draft id must be a positive number")
		}
		d, err := st.Draft(0, id)
		if err != nil {
			return err
		}
		if d == nil {
			return fmt.Errorf("draft %d does not exist", id)
		}
		return fn(st, d)
	})
}

func postOf(d *store.Draft) publish.Post {
	return publish.Post{Kind: d.Kind, Community: deref(d.Community), Title: deref(d.Title), Body: d.Body,
		ReplyTo: deref(d.ReplyTo), IdempotencyKey: fmt.Sprintf("radaro-draft-%d", d.ID)}
}

func validateDraft(d *store.Draft) error {
	pl, ok := publish.LookupPlatform(d.Platform)
	if !ok {
		return fmt.Errorf("unknown platform %q", d.Platform)
	}
	return pl.Validate(postOf(d))
}

// accountFor picks the draft's account, or the only one for its platform.
func accountFor(st *store.Store, d *store.Draft) (*store.Account, error) {
	if d.AccountID != nil {
		acc, err := st.Account(0, *d.AccountID)
		if err != nil {
			return nil, err
		}
		if acc == nil {
			return nil, fmt.Errorf("account %d no longer exists", *d.AccountID)
		}
		return acc, nil
	}
	accs, err := st.Accounts(0, d.Platform)
	if err != nil {
		return nil, err
	}
	switch len(accs) {
	case 0:
		return nil, fmt.Errorf("no %s account connected; run radaro connect %s", d.Platform, d.Platform)
	case 1:
		return accs[0], nil
	}
	return nil, fmt.Errorf("several %s accounts are connected; create the draft with --account", d.Platform)
}

func mentionByID(st *store.Store, id string) (*model.Mention, error) {
	ms, err := st.Mentions(store.MentionFilter{})
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if m.ID == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("mention %s not found", id)
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

func draftSummary(d *store.Draft) string {
	where := d.Platform
	if d.Community != nil && d.Platform == "reddit" {
		where += " r/" + strings.TrimPrefix(*d.Community, "r/")
	}
	if d.Kind == "reply" {
		return where + " reply to " + deref(d.ReplyTo)
	}
	if d.Title != nil {
		return where + " post “" + clip(*d.Title, 60) + "”"
	}
	return where + " post “" + clip(d.Body, 60) + "”"
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
