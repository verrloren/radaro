package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/analyze"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/sampledata"
	"github.com/verrloren/radaro/internal/store"
)

func (a *app) demoCmd() *cobra.Command {
	var (
		serve bool
		port  int
		host  string
	)
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Load a bundled synthetic dataset and show the full pipeline — no keys, no network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			fmt.Println("Radaro demo: loading a bundled SYNTHETIC dataset (a fictional app, fictional accounts)")
			fmt.Println("and running the real pipeline: aggregate → sentiment → themes.")
			mentions := sampledata.Mentions(time.Now())
			lex := analyze.NewLexicon()
			for _, m := range mentions {
				r := lex.Score(m.Content())
				m.Sentiment, m.SentimentScore = r.Label, &r.Score
			}
			if _, err := st.Upsert(mentions, false); err != nil {
				return err
			}
			if err := st.SaveTracking(sampledata.Query, []string{"hackernews", "reddit", "mastodon", "bluesky"}, 0); err != nil {
				return err
			}
			if err := reclusterAndPrint(st, sampledata.Query); err != nil {
				return err
			}
			if !serve {
				return nil
			}
			return a.runServer(cmd.Context(), st, host, port)
		},
	}
	cmd.Flags().BoolVar(&serve, "serve", true, "launch the dashboard after loading")
	cmd.Flags().IntVar(&port, "port", 8042, "dashboard port")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "bind host")
	return cmd
}

type scanFlags struct {
	sources string
	limit   int
	pages   int
}

func (f *scanFlags) register(cmd *cobra.Command, pagesHelp string) {
	cmd.Flags().StringVar(&f.sources, "sources", "", "comma-separated sources (default: $RADARO_SOURCES or hackernews,bluesky)")
	cmd.Flags().IntVar(&f.limit, "limit", 50, "max items per source page (1-100)")
	cmd.Flags().IntVar(&f.pages, "pages", 3, pagesHelp)
}

func (a *app) applyScanFlags(f *scanFlags) error {
	if f.limit < 1 || f.limit > 100 {
		return errors.New("--limit must be between 1 and 100")
	}
	if f.pages < 1 || f.pages > 20 {
		return errors.New("--pages must be between 1 and 20")
	}
	a.cfg.PerSourceLimit = f.limit
	if f.sources != "" {
		a.cfg.Sources = splitSources(f.sources)
		if len(a.cfg.Sources) == 0 {
			return errors.New("provide at least one source")
		}
	}
	return nil
}

func (a *app) trackCmd() *cobra.Command {
	var (
		f       scanFlags
		project int64
	)
	cmd := &cobra.Command{
		Use:   "track <keyword>",
		Short: "Fetch live mentions for a keyword, analyze and store them",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := cleanQuery(args[0])
			if err != nil {
				return err
			}
			if err := a.applyScanFlags(&f); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			if !a.jsonFlag {
				fmt.Printf("Listening for “%s” across: %s …\n", query, joinComma(a.cfg.Sources))
			}
			res, err := pipeline.New(a.cfg, st).Track(cmd.Context(), query, pipeline.Options{Pages: f.pages, ProjectID: project})
			if err != nil {
				return err
			}
			if a.jsonFlag {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				printScanProblems(res)
				printAlertResult(res)
				fmt.Printf("✓ %d fetched · %d new\n", res.Fetched, res.New)
				if err := printReport(st, query); err != nil {
					return err
				}
				fmt.Printf("\nView the dashboard: radaro serve --db %s\n", a.cfg.DBPath)
			}
			if len(res.Errors) == len(a.cfg.Sources) {
				return exitError{1} // every configured source failed
			}
			return nil
		},
	}
	f.register(cmd, "max continuation pages when catching up new mentions (1-20)")
	cmd.Flags().Int64Var(&project, "project", 0, "add this keyword to a project by numeric ID")
	return cmd
}

func (a *app) backfillCmd() *cobra.Command {
	var f scanFlags
	cmd := &cobra.Command{
		Use:   "backfill <keyword>",
		Short: "Fetch older result pages using durable per-source cursors",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := cleanQuery(args[0])
			if err != nil {
				return err
			}
			if err := a.applyScanFlags(&f); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			if f.sources == "" {
				if t, err := st.Tracking(query); err != nil {
					return err
				} else if t != nil && len(t.Sources) > 0 {
					a.cfg.Sources = t.Sources
				}
			}
			if !a.jsonFlag {
				fmt.Printf("Backfilling “%s” across: %s (up to %d pages each) …\n", query, joinComma(a.cfg.Sources), f.pages)
			}
			res, err := pipeline.New(a.cfg, st).Track(cmd.Context(), query, pipeline.Options{Backfill: true, Pages: f.pages})
			if err != nil {
				return err
			}
			if a.jsonFlag {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				printScanProblems(res)
				for _, src := range a.cfg.Sources {
					done := ""
					if res.BackfillComplete[src] {
						done = " · history complete"
					}
					fmt.Printf("  %s: %d matched from %d page(s)%s\n", src, res.BySource[src], res.PagesBySource[src], done)
				}
				fmt.Printf("✓ %d fetched · %d historical mentions added\n", res.Fetched, res.New)
			}
			if len(res.Errors) == len(a.cfg.Sources) {
				return exitError{1}
			}
			return nil
		},
	}
	f.register(cmd, "older pages to fetch per source (1-20)")
	return cmd
}

func (a *app) watchCmd() *cobra.Command {
	var (
		f     scanFlags
		every int
		runs  int
	)
	cmd := &cobra.Command{
		Use:   "watch <keyword>",
		Short: "Scan a keyword on an interval until stopped",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := cleanQuery(args[0])
			if err != nil {
				return err
			}
			if err := a.applyScanFlags(&f); err != nil {
				return err
			}
			if every < 30 {
				return errors.New("--every must be at least 30 seconds")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			fmt.Printf("Watching “%s” every %ds across: %s (Ctrl-C to stop)\n", query, every, joinComma(a.cfg.Sources))
			p := pipeline.New(a.cfg, st)
			for n := 1; ; n++ {
				res, err := p.Track(ctx, query, pipeline.Options{Pages: f.pages})
				switch {
				case ctx.Err() != nil:
					fmt.Println("\nStopped watching.")
					return nil
				case err != nil: // a single failed scan must not kill the watcher
					fmt.Printf("  ✗ scan %d failed: %v\n", n, err)
				default:
					printScanProblems(res)
					printAlertResult(res)
					fmt.Printf("✓ scan %d: %d fetched · %d new\n", n, res.Fetched, res.New)
				}
				if runs > 0 && n >= runs {
					return nil
				}
				select {
				case <-ctx.Done():
					fmt.Println("\nStopped watching.")
					return nil
				case <-time.After(time.Duration(every) * time.Second):
				}
			}
		},
	}
	f.register(cmd, "max continuation pages when a scan has fallen behind (1-20)")
	cmd.Flags().IntVar(&every, "every", 900, "seconds between scans (minimum 30)")
	cmd.Flags().IntVar(&runs, "runs", 0, "stop after this many scans (default: keep running)")
	return cmd
}

func printScanProblems(res *pipeline.Result) {
	names := make([]string, 0, len(res.Errors))
	for n := range res.Errors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("  ! %s: %s\n", n, res.Errors[n])
	}
	for src, n := range res.RetryCounts {
		fmt.Printf("  ↻ %s: %d retries\n", src, n)
	}
	if res.AnalysisError != nil {
		fmt.Printf("  ! optional LLM labels: %s\n", *res.AnalysisError)
	}
	if res.SentimentError != nil {
		fmt.Printf("  ! sentiment: %s\n", *res.SentimentError)
	}
}

func printAlertResult(res *pipeline.Result) {
	if res.Alerted > 0 {
		fmt.Printf("  ↗ delivered %d negative-mention alerts\n", res.Alerted)
	}
	if res.AlertError != nil {
		pending := ""
		if n := res.AlertPending + res.ThresholdPending; n > 0 {
			pending = fmt.Sprintf(" (%d queued for retry)", n)
		}
		fmt.Printf("  ! alert delivery: %s%s\n", *res.AlertError, pending)
	}
	if res.ThresholdAlerted > 0 {
		fmt.Printf("  ↗ delivered %d metric alert(s): %s\n", res.ThresholdAlerted, joinComma(res.ThresholdEvents))
	}
}

// reclusterAndPrint recomputes themes for query, persists labels, and prints the report.
func reclusterAndPrint(st *store.Store, query string) error {
	mentions, err := st.Mentions(store.MentionFilter{Scope: store.Scope{Query: query}})
	if err != nil {
		return err
	}
	analyze.NewThemeExtractor().Extract(mentions)
	if _, err := st.Upsert(mentions, true); err != nil {
		return err
	}
	return printReport(st, query)
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func stderr(format string, args ...any) { fmt.Fprintf(os.Stderr, format, args...) }
