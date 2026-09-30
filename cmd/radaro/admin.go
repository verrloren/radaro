package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/auth"
	"github.com/verrloren/radaro/internal/store"
)

// adminCmd groups commands that work on the database directly, so they run
// on the server itself.
func (a *app) adminCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Server-side maintenance (runs against the database directly)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "backup <path>",
		Short: "Write a consistent snapshot of the database to a new file (safe while serving)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withStore(func(st *store.Store, args []string) error {
			if err := st.Backup(a.ctx(), args[0]); err != nil {
				return err
			}
			info, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(map[string]any{"path": args[0], "bytes": info.Size()})
			}
			fmt.Printf("✓ backed up %s → %s (%d bytes)\n", st.Path(), args[0], info.Size())
			return nil
		}),
	})
	cmd.AddCommand(&cobra.Command{
		Use: "users", Short: "List the accounts registered on this server", Args: cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			users, err := st.Users()
			if err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(users)
			}
			fmt.Printf("%4s  %-40s %s\n", "ID", "EMAIL", "SINCE")
			for _, u := range users {
				role := ""
				if u.IsAdmin {
					role = " (admin)"
				}
				fmt.Printf("%4d  %-40s %s\n", u.ID, u.Email+role, u.CreatedAt[:10])
			}
			return nil
		}),
	})
	var email string
	var fromStdin bool
	reset := &cobra.Command{
		Use:   "reset-password",
		Short: "Set a new password for a user and sign out all their sessions",
		Args:  cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			u, err := st.UserByEmail(email)
			if err != nil {
				return err
			}
			if u == nil {
				return fmt.Errorf("no user with email %q", email)
			}
			pw, err := readPassword(fromStdin, true)
			if err != nil {
				return err
			}
			hash, err := auth.HashPassword(pw)
			if err != nil {
				return err
			}
			if err := st.SetPassword(u.ID, hash); err != nil {
				return err
			}
			fmt.Printf("✓ new password set for %s; their sessions are signed out\n", u.Email)
			return nil
		}),
	}
	reset.Flags().StringVar(&email, "email", "", "the user's email")
	reset.Flags().BoolVar(&fromStdin, "password-stdin", false, "read the new password from stdin")
	_ = reset.MarkFlagRequired("email")
	cmd.AddCommand(reset)
	return cmd
}
