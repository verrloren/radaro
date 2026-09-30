// Command radaro is a self-hosted social-listening tool: track what the
// internet says about a keyword across Hacker News, Reddit, Bluesky, Mastodon,
// Stack Overflow, RSS, X and YouTube, with sentiment and themes, in one binary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/store"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

type app struct {
	dbFlag   string
	jsonFlag bool
	cfg      *config.Config
	cmdCtx   context.Context
}

// ctx is the running command's context (cancelled on Ctrl-C).
func (a *app) ctx() context.Context {
	if a.cmdCtx == nil {
		return context.Background()
	}
	return a.cmdCtx
}

// exitError carries a non-zero exit code without an extra error message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := newRoot().ExecuteContext(ctx)
	var ee exitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		os.Exit(ee.code)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "radaro",
		Short:         "Radaro — self-hosted social listening. Hear what the internet says about you.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if a.dbFlag != "" {
				cfg.DBPath = a.dbFlag
			}
			a.cfg = cfg
			a.cmdCtx = cmd.Context()
			return nil
		},
	}
	root.PersistentFlags().StringVar(&a.dbFlag, "db", "", "database path (default: radaro.db, or $RADARO_DB)")
	root.PersistentFlags().BoolVar(&a.jsonFlag, "json", false, "print machine-readable JSON")
	root.AddCommand(
		a.demoCmd(), a.trackCmd(), a.backfillCmd(), a.watchCmd(), a.reportCmd(), a.serveCmd(),
		a.sourcesCmd(), a.projectCmd(), a.exportCmd(), a.testAlertCmd(),
		a.connectCmd(), a.accountsCmd(), a.opportunitiesCmd(), a.draftCmd(), a.publishCmd(),
		a.statsCmd(), a.activityCmd(), a.statusCmd(), a.skillCmd(), a.adminCmd(),
	)
	return root
}

func (a *app) openStore() (*store.Store, error) {
	return store.Open(a.cfg.DBPath)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func cleanQuery(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", errors.New("query must not be empty")
	}
	return q, nil
}

func splitSources(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
