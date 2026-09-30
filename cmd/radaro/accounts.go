package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/store"
)

const noAccountsHint = "No accounts yet. Connect one with: radaro connect bluesky|mastodon|devto|reddit"

// limitsView, quotaView and accountView decode the server's AccountView for
// text output; --json prints the server's answer as it came.
type limitsView struct {
	Daily              int  `json:"daily"`
	MinIntervalSec     int  `json:"min_interval_sec"`
	CommunityCooldownH int  `json:"community_cooldown_h"`
	Custom             bool `json:"custom"`
}

type quotaView struct {
	Daily     int        `json:"daily"`
	Used24h   int        `json:"used_24h"`
	Remaining int        `json:"remaining"`
	NextAt    *time.Time `json:"next_at"`
	Reason    string     `json:"reason"`
	Ready     bool       `json:"ready"`
}

type accountView struct {
	store.Account
	Limits *limitsView `json:"limits"`
	Quota  *quotaView  `json:"quota"` // absent when the server could not compute it
}

// accountPatch is the body of PATCH /api/accounts/{id}; nil fields stay as they are.
type accountPatch struct {
	Paused             *bool `json:"paused,omitempty"`
	DailyLimit         *int  `json:"daily_limit,omitempty"`
	MinIntervalSec     *int  `json:"min_interval_sec,omitempty"`
	CommunityCooldownH *int  `json:"community_cooldown_h,omitempty"`
}

func accountPath(id int64, suffix string) string {
	return "/api/accounts/" + strconv.FormatInt(id, 10) + suffix
}

func accountID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("account id must be a positive number")
	}
	return id, nil
}

// accountErr names the account when the server does not know it.
func accountErr(err error, id int64) error {
	if client.StatusOf(err) == http.StatusNotFound {
		return fmt.Errorf("account %d does not exist", id)
	}
	return err
}

// call sends a request and returns the raw answer, also decoded into out when
// out is not nil.
func call(ctx context.Context, c *client.Client, method, path string, body, out any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Do(ctx, method, path, nil, body, &raw); err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("unexpected answer from %s: %w", path, err)
		}
	}
	return raw, nil
}

func (a *app) accountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "Publishing accounts: health, quota and limits",
		Long: "List your connected publishing accounts with their health (live, unknown, limited, invalid,\n" +
			"suspended), whether they are paused, their limits and where they stand against them.\n" +
			"With --json, quota.next_at is when an account may publish next (null = now, or never without\n" +
			"a person when quota.ready is false and quota.reason says why).",
		Args: cobra.NoArgs,
		RunE: a.apiCmd(a.listAccounts),
	}
	cmd.AddCommand(a.accountsCheckCmd(), a.accountsPauseCmd(true), a.accountsPauseCmd(false),
		a.accountsLimitsCmd(), a.accountsRemoveCmd())
	return cmd
}

func (a *app) listAccounts(ctx context.Context, c *client.Client, _ []string) error {
	raw, views, err := fetchAccounts(ctx, c)
	if err != nil {
		return err
	}
	if a.jsonFlag {
		return printJSON(raw)
	}
	printAccountsOrHint(views)
	return nil
}

// fetchAccounts is GET /api/accounts: the AccountView array as sent (never
// null) and decoded.
func fetchAccounts(ctx context.Context, c *client.Client) (json.RawMessage, []accountView, error) {
	var res struct {
		Accounts json.RawMessage `json:"accounts"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/accounts", nil, nil, &res); err != nil {
		return nil, nil, err
	}
	return decodeViews(res.Accounts)
}

func decodeViews(raw json.RawMessage) (json.RawMessage, []accountView, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]"), nil, nil
	}
	var views []accountView
	if err := json.Unmarshal(raw, &views); err != nil {
		return nil, nil, fmt.Errorf("unexpected list of accounts: %w", err)
	}
	return raw, views, nil
}

// findAccount is one account from the list, which is the only way to read a
// single account's view.
func findAccount(ctx context.Context, c *client.Client, id int64) (json.RawMessage, *accountView, error) {
	var res struct {
		Accounts []json.RawMessage `json:"accounts"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/accounts", nil, nil, &res); err != nil {
		return nil, nil, err
	}
	for _, raw := range res.Accounts {
		var v accountView
		if json.Unmarshal(raw, &v) == nil && v.ID == id {
			return raw, &v, nil
		}
	}
	return nil, nil, fmt.Errorf("account %d does not exist", id)
}

func (a *app) accountsCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check [id]",
		Short: "Check that accounts still work (credentials, suspension, rate limits)",
		Long: "Check one account, or all of yours, with the platform. A check that could not run leaves\n" +
			"the status unchanged and is reported; checking one account then exits with status 1.",
		Args: cobra.MaximumNArgs(1),
		RunE: a.apiCmd(a.checkAccounts),
	}
}

func (a *app) checkAccounts(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 1 {
		return a.checkAccount(ctx, c, args[0])
	}
	var res struct {
		Accounts json.RawMessage `json:"accounts"`
		Error    string          `json:"error"`
		Errors   []string        `json:"errors"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/accounts/check", nil, nil, &res); err != nil {
		return err
	}
	problems := append([]string{}, res.Errors...)
	if res.Error != "" {
		problems = append(problems, res.Error)
	}
	raw, views, err := decodeViews(res.Accounts)
	if err != nil {
		return err
	}
	return a.printCheck(raw, views, problems)
}

// checkAccount checks one account. Its outcome is a question the caller wants
// answered with the exit code, so a check that could not run exits with 1.
func (a *app) checkAccount(ctx context.Context, c *client.Client, arg string) error {
	id, err := accountID(arg)
	if err != nil {
		return err
	}
	v := &accountView{}
	raw, err := call(ctx, c, http.MethodPost, accountPath(id, "/check"), nil, v)
	problems := []string{}
	if client.StatusOf(err) == http.StatusBadGateway {
		problems = append(problems, fmt.Sprintf("account %d: %v (status left unchanged)", id, err))
		raw, v, err = findAccount(ctx, c, id)
	}
	if err != nil {
		return accountErr(err, id)
	}
	list := json.RawMessage("[" + string(raw) + "]")
	if err := a.printCheck(list, []accountView{*v}, problems); err != nil {
		return err
	}
	if len(problems) > 0 {
		return exitError{1}
	}
	return nil
}

func (a *app) printCheck(raw json.RawMessage, views []accountView, problems []string) error {
	if a.jsonFlag {
		return printJSON(map[string]any{"accounts": raw, "errors": problems})
	}
	for _, p := range problems {
		fmt.Printf("  ! %s\n", p)
	}
	printAccountsOrHint(views)
	return nil
}

func (a *app) accountsPauseCmd(paused bool) *cobra.Command {
	use, short, note := "resume <id>", "Let a paused account publish again", "resumed"
	if paused {
		use, short, note = "pause <id>", "Stop an account from publishing (auto-pick skips it)", "paused; it will not publish until resumed"
	}
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			return a.patchAccount(ctx, c, args[0], accountPatch{Paused: &paused}, note)
		}),
	}
}

func (a *app) patchAccount(ctx context.Context, c *client.Client, arg string, patch accountPatch, note string) error {
	id, err := accountID(arg)
	if err != nil {
		return err
	}
	var v accountView
	raw, err := call(ctx, c, http.MethodPatch, accountPath(id, ""), patch, &v)
	if err != nil {
		return accountErr(err, id)
	}
	return a.showAccount(raw, &v, note)
}

func (a *app) showAccount(raw json.RawMessage, v *accountView, note string) error {
	if a.jsonFlag {
		return printJSON(raw)
	}
	fmt.Printf("✓ account %d (%s %s) %s\n", v.ID, v.Platform, v.Handle, note)
	fmt.Printf("  limits: %s\n", limitsLine(v.Limits))
	return nil
}

// limitFlags are the values of `accounts limits`; only the flags given change.
type limitFlags struct {
	daily              int
	interval, cooldown time.Duration
}

func (l limitFlags) patch(changed func(string) bool) (accountPatch, error) {
	var p accountPatch
	if changed("daily") {
		if l.daily < 0 {
			return p, errors.New("--daily must not be negative")
		}
		p.DailyLimit = &l.daily
	}
	if changed("interval") {
		if l.interval < 0 || (l.interval > 0 && l.interval < time.Second) {
			return p, errors.New("--interval must be 0 or at least 1s")
		}
		sec := int(l.interval / time.Second)
		p.MinIntervalSec = &sec
	}
	if changed("cooldown") {
		if l.cooldown < 0 || l.cooldown%time.Hour != 0 {
			return p, errors.New("--cooldown must be whole hours (e.g. 24h), or 0 for the default")
		}
		h := int(l.cooldown / time.Hour)
		p.CommunityCooldownH = &h
	}
	return p, nil
}

func (a *app) accountsLimitsCmd() *cobra.Command {
	var l limitFlags
	cmd := &cobra.Command{
		Use:   "limits <id>",
		Short: "Show or set an account's own publishing limits (0 = the platform default)",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
		p, err := l.patch(cmd.Flags().Changed)
		if err != nil {
			return err
		}
		if p != (accountPatch{}) {
			return a.patchAccount(ctx, c, args[0], p, "limits updated")
		}
		id, err := accountID(args[0])
		if err != nil {
			return err
		}
		raw, v, err := findAccount(ctx, c, id)
		if err != nil {
			return err
		}
		return a.showAccount(raw, v, "limits")
	})
	cmd.Flags().IntVar(&l.daily, "daily", 0, "publications per rolling 24 hours (0 = platform default)")
	cmd.Flags().DurationVar(&l.interval, "interval", 0, "minimum time between two publications, e.g. 10m (0 = default)")
	cmd.Flags().DurationVar(&l.cooldown, "cooldown", 0, "minimum time between two posts in one community, whole hours, e.g. 24h (0 = default)")
	return cmd
}

func (a *app) accountsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use: "remove <id>", Short: "Forget a connected account and its credentials", Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := accountID(args[0])
			if err != nil {
				return err
			}
			if err := c.Do(ctx, http.MethodDelete, accountPath(id, ""), nil, nil, nil); err != nil {
				return accountErr(err, id)
			}
			fmt.Printf("✓ removed account %d\n", id)
			return nil
		}),
	}
}

func printAccountsOrHint(views []accountView) {
	if len(views) == 0 {
		fmt.Println(noAccountsHint)
		return
	}
	printAccounts(views)
}

func printAccounts(views []accountView) {
	fmt.Printf("%4s  %-9s %-24s %-10s %-6s %s\n", "ID", "PLATFORM", "HANDLE", "STATUS", "QUOTA", "NEXT")
	anyPaused := false
	for _, v := range views {
		status := v.Status
		if v.Paused {
			status += "*"
			anyPaused = true
		}
		fmt.Printf("%4d  %-9s %-24s %-10s %-6s %s\n", v.ID, v.Platform, clip(v.Handle, 24), status, quotaText(v.Quota), nextText(v.Quota))
		if detail := deref(v.StatusDetail); detail != "" && (v.Quota == nil || !strings.Contains(v.Quota.Reason, detail)) {
			fmt.Printf("      %s\n", clip(detail, 100))
		}
	}
	if anyPaused {
		fmt.Println("* paused: resume with radaro accounts resume <id>")
	}
}

func quotaText(q *quotaView) string {
	if q == nil {
		return "—"
	}
	return fmt.Sprintf("%d/%d", q.Remaining, q.Daily)
}

// nextText is when the account may publish next, and why not now.
func nextText(q *quotaView) string {
	switch {
	case q == nil:
		return "—"
	case q.Ready:
		return "now"
	}
	next := "blocked"
	if q.NextAt != nil {
		next = when(*q.NextAt)
	}
	if q.Reason != "" {
		next += " — " + q.Reason
	}
	return next
}

func when(t time.Time) string {
	return t.Local().Format("Jan 2 15:04")
}

func limitsLine(l *limitsView) string {
	if l == nil {
		return "unknown"
	}
	s := fmt.Sprintf("%d per 24h, %s apart", l.Daily, time.Duration(l.MinIntervalSec)*time.Second)
	if l.CommunityCooldownH > 0 {
		s += fmt.Sprintf(", %dh per community", l.CommunityCooldownH)
	}
	if !l.Custom {
		s += " (platform defaults)"
	}
	return s
}
