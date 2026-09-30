package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/client"
)

// projectPool is one platform's entry of GET /api/projects/{id}/accounts.
type projectPool struct {
	Platform struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"platform"`
	Accounts []accountView `json:"accounts"`
}

// poolCall sends a request whose answer is the project's pools, and prints
// them for --json; printed reports whether it did.
func (a *app) poolCall(ctx context.Context, c *client.Client, method, path string, body any) (pools []projectPool, printed bool, err error) {
	raw, err := call(ctx, c, method, path, body, &pools)
	if err != nil {
		return nil, false, err
	}
	if a.jsonFlag {
		return pools, true, printJSON(raw)
	}
	return pools, false, nil
}

func (a *app) projectAccountsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "accounts <project-id>",
		Short: "The pool of accounts a project publishes (and scans) with on each platform",
		Long: "List each platform's pool. Publishing a draft without its own account picks one from the\n" +
			"pool, within its limits; an empty pool means any of your accounts on that platform.",
		Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			pools, printed, err := a.poolCall(ctx, c, http.MethodGet, projectPath(id, "/accounts"), nil)
			if err != nil || printed {
				return err
			}
			printProjectPools(id, pools)
			return nil
		}),
	}
}

func printProjectPools(id int64, pools []projectPool) {
	for _, p := range pools {
		if len(p.Accounts) == 0 {
			fmt.Printf("  %-9s —  any of your %s accounts (add one: radaro project bind %d %s <account-id>)\n",
				p.Platform.Label, p.Platform.Name, id, p.Platform.Name)
			continue
		}
		label := p.Platform.Label
		for i := range p.Accounts {
			acc := &p.Accounts[i]
			fmt.Printf("  %-9s #%d %s  %s\n", label, acc.ID, acc.Handle, poolState(acc))
			label = ""
		}
	}
}

// poolState is an account's health and quota in one short phrase.
func poolState(v *accountView) string {
	parts := []string{v.Status}
	if v.Paused {
		parts = append(parts, "paused")
	}
	if v.Quota != nil {
		parts = append(parts, fmt.Sprintf("%d/%d left", v.Quota.Remaining, v.Quota.Daily))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func poolSize(pools []projectPool, platform string) int {
	for _, p := range pools {
		if p.Platform.Name == platform {
			return len(p.Accounts)
		}
	}
	return 0
}

func (a *app) projectBindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bind <project-id> <platform> <account-id>",
		Short: "Add an account to a project's pool for a platform (see radaro accounts)",
		Args:  cobra.ExactArgs(3),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			acc, err := accountID(args[2])
			if err != nil {
				return err
			}
			pools, printed, err := a.poolCall(ctx, c, http.MethodPut, projectPath(id, "/accounts/", args[1]), map[string]int64{"account_id": acc})
			if err != nil || printed {
				return err
			}
			fmt.Printf("✓ project %d publishes on %s with account %d (%d in its pool)\n", id, args[1], acc, poolSize(pools, args[1]))
			return nil
		}),
	}
}

func (a *app) projectUnbindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unbind <project-id> <platform> [account-id]",
		Short: "Remove an account from a project's pool, or empty the platform's pool",
		Long: "Remove one account from a project's pool for a platform, or all of them when no account\n" +
			"is given. With an empty pool the project publishes with any of your accounts on that platform.",
		Args: cobra.RangeArgs(2, 3),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			path, done := projectPath(id, "/accounts/", args[1]), fmt.Sprintf("emptied project %d's %s pool", id, args[1])
			if len(args) == 3 {
				acc, err := accountID(args[2])
				if err != nil {
					return err
				}
				path += "/" + strconv.FormatInt(acc, 10)
				done = fmt.Sprintf("removed account %d from project %d's %s pool", acc, id, args[1])
			}
			pools, printed, err := a.poolCall(ctx, c, http.MethodDelete, path, nil)
			if err != nil || printed {
				return err
			}
			fmt.Printf("✓ %s (%d left)\n", done, poolSize(pools, args[1]))
			return nil
		}),
	}
}
