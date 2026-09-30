package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
)

// apiCmd runs fn with the signed-in client.
func (a *app) apiCmd(fn func(ctx context.Context, c *client.Client, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c, err := a.api()
		if err != nil {
			return err
		}
		return fn(cmd.Context(), c, args)
	}
}

func projectPath(id int64, rest ...string) string {
	return "/api/projects/" + strconv.FormatInt(id, 10) + strings.Join(rest, "")
}

func (a *app) projectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Your projects: keywords and the pool of accounts each project publishes with"}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List your projects and their sizes", Args: cobra.NoArgs,
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, _ []string) error {
				var ps []store.Project
				if err := c.Do(ctx, "GET", "/api/projects", nil, nil, &ps); err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(ps)
				}
				fmt.Printf("%4s  %-30s %9s %9s\n", "ID", "NAME", "KEYWORDS", "MENTIONS")
				for _, p := range ps {
					name := p.Name
					if p.IsDefault {
						name += " (default)"
					}
					fmt.Printf("%4d  %-30s %9d %9d\n", p.ID, name, p.QueryCount, p.MentionCount)
				}
				return nil
			}),
		},
		&cobra.Command{
			Use: "create <name>", Short: "Create an empty project", Args: cobra.ExactArgs(1),
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
				var p store.Project
				if err := c.Do(ctx, "POST", "/api/projects", nil, map[string]string{"name": args[0]}, &p); err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(p)
				}
				fmt.Printf("✓ created project %s (id %d)\n", p.Name, p.ID)
				return nil
			}),
		},
		&cobra.Command{
			Use: "rename <project-id> <name>", Short: "Rename a project", Args: cobra.ExactArgs(2),
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				var p store.Project
				if err := c.Do(ctx, "PATCH", projectPath(id), nil, map[string]string{"name": args[1]}, &p); err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(p)
				}
				fmt.Printf("✓ project %d is now %s\n", p.ID, p.Name)
				return nil
			}),
		},
		&cobra.Command{
			Use: "keywords <project-id>", Short: "List a project's keywords", Args: cobra.ExactArgs(1),
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				var ks []store.Keyword
				if err := c.Do(ctx, "GET", projectPath(id, "/keywords"), nil, nil, &ks); err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(ks)
				}
				if len(ks) == 0 {
					fmt.Printf("No keywords yet. Add some: radaro project add %d \"keyword\" …\n", id)
					return nil
				}
				fmt.Printf("%5s  %-34s %9s  %s\n", "ID", "KEYWORD", "MENTIONS", "SOURCES")
				for _, k := range ks {
					fmt.Printf("%5d  %-34s %9d  %s\n", k.ID, clip(k.Query, 34), k.MentionCount, strings.Join(k.Sources, ", "))
				}
				return nil
			}),
		},
		a.projectAddCmd(),
		&cobra.Command{
			Use: "remove <project-id> <keyword>", Short: "Remove a keyword from a project (its mentions are kept)", Args: cobra.ExactArgs(2),
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				k, err := findKeyword(ctx, c, id, args[1])
				if err != nil {
					return err
				}
				if err := c.Do(ctx, "DELETE", projectPath(id, "/keywords/", strconv.FormatInt(k.ID, 10)), nil, nil, nil); err != nil {
					return err
				}
				fmt.Printf("✓ removed %q from project %d\n", k.Query, id)
				return nil
			}),
		},
		&cobra.Command{
			Use: "report <project-id>", Short: "Print aggregate sentiment and themes across a project", Args: cobra.ExactArgs(1),
			RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				var p store.Project
				if err := c.Do(ctx, "GET", projectPath(id), nil, nil, &p); err != nil {
					return err
				}
				var r server.Report
				if err := c.Do(ctx, "GET", "/api/report", url.Values{"p": {args[0]}}, nil, &r); err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(map[string]any{"project": p, "report": r})
				}
				fmt.Printf("\n%s — %d keywords · %d mentions\n", p.Name, p.QueryCount, r.Summary.Total)
				printReportBody(&r)
				return nil
			}),
		},
		a.projectDeleteCmd(),
		a.projectAccountsCmd(),
		a.projectBindCmd(),
		a.projectUnbindCmd(),
	)
	return cmd
}

func (a *app) projectAddCmd() *cobra.Command {
	var srcs string
	cmd := &cobra.Command{
		Use: "add <project-id> <keyword>...", Short: "Add keywords to a project (scan them with radaro track)", Args: cobra.MinimumNArgs(2),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			body := map[string]any{"queries": args[1:]}
			if srcs != "" {
				body["sources"] = splitSources(srcs)
			}
			var res struct {
				Added    int             `json:"added"`
				Keywords []store.Keyword `json:"keywords"`
			}
			if err := c.Do(ctx, "POST", projectPath(id, "/keywords"), nil, body, &res); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(res)
			}
			fmt.Printf("✓ added %d keyword(s) to project %d (%d already there)\n", res.Added, id, len(args)-1-res.Added)
			return nil
		}),
	}
	cmd.Flags().StringVar(&srcs, "sources", "", "comma-separated sources for new keywords (default: the server's RADARO_SOURCES)")
	return cmd
}

func (a *app) projectDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use: "delete <project-id>", Short: "Delete a project (mentions of its keywords are kept)", Args: cobra.ExactArgs(1),
		RunE: a.apiCmd(func(ctx context.Context, c *client.Client, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			if !yes {
				return errors.New("pass --yes to delete the project")
			}
			if err := c.Do(ctx, "DELETE", projectPath(id), nil, nil, nil); err != nil {
				return err
			}
			fmt.Printf("✓ deleted project %d\n", id)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	return cmd
}

// findKeyword looks a keyword up by text, or by id when the argument is one.
func findKeyword(ctx context.Context, c *client.Client, projectID int64, arg string) (*store.Keyword, error) {
	var ks []store.Keyword
	if err := c.Do(ctx, "GET", projectPath(projectID, "/keywords"), nil, nil, &ks); err != nil {
		return nil, err
	}
	arg = strings.TrimSpace(arg)
	for i, k := range ks {
		if strings.EqualFold(k.Query, arg) || strconv.FormatInt(k.ID, 10) == arg {
			return &ks[i], nil
		}
	}
	return nil, fmt.Errorf("%q is not in project %d", arg, projectID)
}

func (a *app) withStore(fn func(*store.Store, []string) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		st, err := a.openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		return fn(st, args)
	}
}

func projectID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("project id must be a positive integer")
	}
	return id, nil
}
