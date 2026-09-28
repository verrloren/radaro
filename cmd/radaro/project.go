package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/store"
)

func (a *app) projectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Group tracked keywords and report across them"}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List projects and their sizes", Args: cobra.NoArgs,
			RunE: a.withStore(func(st *store.Store, _ []string) error {
				ps, err := st.Projects()
				if err != nil {
					return err
				}
				if a.jsonFlag {
					return printJSON(ps)
				}
				fmt.Printf("%4s  %-30s %9s %9s\n", "ID", "NAME", "KEYWORDS", "MENTIONS")
				for _, p := range ps {
					fmt.Printf("%4d  %-30s %9d %9d\n", p.ID, p.Name, p.QueryCount, p.MentionCount)
				}
				return nil
			}),
		},
		&cobra.Command{
			Use: "create <name>", Short: "Create an empty project", Args: cobra.ExactArgs(1),
			RunE: a.withStore(func(st *store.Store, args []string) error {
				p, err := st.CreateProject(args[0])
				if err != nil {
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
			Use: "add <project-id> <keyword>", Short: "Add a tracked keyword to a project", Args: cobra.ExactArgs(2),
			RunE: a.withStore(func(st *store.Store, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				added, err := st.AddQueryToProject(id, args[1])
				if err != nil {
					return err
				}
				verb := "added to"
				if !added {
					verb = "already belongs to"
				}
				fmt.Printf("✓ “%s” %s project %d\n", strings.TrimSpace(args[1]), verb, id)
				return nil
			}),
		},
		&cobra.Command{
			Use: "remove <project-id> <keyword>", Short: "Remove a keyword from a project (data is kept)", Args: cobra.ExactArgs(2),
			RunE: a.withStore(func(st *store.Store, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				removed, err := st.RemoveQueryFromProject(id, args[1])
				if err != nil {
					return err
				}
				if !removed {
					return fmt.Errorf("“%s” is not in project %d", strings.TrimSpace(args[1]), id)
				}
				fmt.Printf("✓ removed “%s” from project %d\n", strings.TrimSpace(args[1]), id)
				return nil
			}),
		},
		a.projectDeleteCmd(),
		&cobra.Command{
			Use: "report <project-id>", Short: "Print aggregate sentiment and themes across a project", Args: cobra.ExactArgs(1),
			RunE: a.withStore(func(st *store.Store, args []string) error {
				id, err := projectID(args[0])
				if err != nil {
					return err
				}
				p, err := st.Project(id)
				if err != nil {
					return err
				}
				if p == nil {
					return fmt.Errorf("project %d does not exist", id)
				}
				r, err := buildReport(st, store.Scope{ProjectID: id})
				if err != nil {
					return err
				}
				r.Project = p
				if a.jsonFlag {
					return printJSON(r)
				}
				fmt.Printf("\n%s — %d mentions across %d keyword(s)\n", p.Name, r.Summary.Total, p.QueryCount)
				fmt.Printf("  %8s %s\n", "keywords", strings.Join(p.Queries, ", "))
				printReportBody(r)
				return nil
			}),
		},
	)
	return cmd
}

func (a *app) projectDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use: "delete <project-id>", Short: "Delete a project grouping (keywords and mentions are kept)", Args: cobra.ExactArgs(1),
		RunE: a.withStore(func(st *store.Store, args []string) error {
			id, err := projectID(args[0])
			if err != nil {
				return err
			}
			if !yes {
				fmt.Printf("Would delete project %d; tracked keywords and mentions stay intact. Add --yes to apply.\n", id)
				return nil
			}
			deleted, err := st.DeleteProject(id)
			if err != nil {
				return err
			}
			if !deleted {
				return fmt.Errorf("project %d does not exist", id)
			}
			fmt.Printf("✓ deleted project %d; data was preserved\n", id)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion of the grouping")
	return cmd
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
