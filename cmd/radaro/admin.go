package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

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
	return cmd
}
