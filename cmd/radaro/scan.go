package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/analyze"
	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/sampledata"
	"github.com/verrloren/radaro/internal/server"
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
			if err := st.SaveTracking(0, sampledata.Query, []string{"hackernews", "reddit", "mastodon", "bluesky"}, 0); err != nil {
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
	cmd.Flags().StringVar(&f.sources, "sources", "", "comma-separated sources (default: the keyword's, else the server's RADARO_SOURCES)")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "max items per source page (1-100; default: the server's)")
	cmd.Flags().IntVar(&f.pages, "pages", 3, pagesHelp)
}

// trackRequest is the body of POST /api/track.
type trackRequest struct {
	Query     string   `json:"query"`
	Sources   []string `json:"sources,omitempty"`
	Mode      string   `json:"mode"`
	Pages     int      `json:"pages"`
	Limit     int      `json:"limit,omitempty"`
	ProjectID *int64   `json:"project_id,omitempty"`
}

func (f *scanFlags) request(query, mode string) (trackRequest, error) {
	if f.limit != 0 && (f.limit < 1 || f.limit > 100) {
		return trackRequest{}, errors.New("--limit must be between 1 and 100")
	}
	if f.pages < 1 || f.pages > 20 {
		return trackRequest{}, errors.New("--pages must be between 1 and 20")
	}
	req := trackRequest{Query: query, Mode: mode, Pages: f.pages, Limit: f.limit}
	if f.sources != "" {
		if req.Sources = splitSources(f.sources); len(req.Sources) == 0 {
			return trackRequest{}, errors.New("provide at least one source")
		}
	}
	return req, nil
}

// scan runs one scan on the server. Scans can take minutes.
func (a *app) scan(ctx context.Context, c *client.Client, req trackRequest) (*pipeline.Result, error) {
	var res pipeline.Result
	if err := c.Do(ctx, "POST", "/api/track", nil, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func scannedSources(res *pipeline.Result) []string {
	names := make([]string, 0, len(res.BySource))
	for n := range res.BySource {
		names = append(names, n)
	}
	for n := range res.Errors {
		if _, ok := res.BySource[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

func (a *app) trackCmd() *cobra.Command {
	var (
		f       scanFlags
		project int64
	)
	cmd := &cobra.Command{
		Use:   "track <keyword>",
		Short: "Scan a keyword now on the server: fetch, analyze and store mentions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := cleanQuery(args[0])
			if err != nil {
				return err
			}
			req, err := f.request(query, "incremental")
			if err != nil {
				return err
			}
			if project != 0 {
				req.ProjectID = &project
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			if !a.jsonFlag {
				fmt.Printf("Listening for “%s” …\n", query)
			}
			res, err := a.scan(cmd.Context(), c, req)
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
				if err := a.printReport(cmd.Context(), c, query); err != nil {
					return err
				}
				fmt.Printf("\nView the dashboard: %s\n", c.Server)
			}
			if len(res.Errors) > 0 && len(res.Errors) == len(scannedSources(res)) {
				return exitError{1} // every source failed
			}
			return nil
		},
	}
	f.register(cmd, "max continuation pages when catching up new mentions (1-20)")
	cmd.Flags().Int64Var(&project, "project", 0, "file the keyword under this project (default: already filed, else Default)")
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
			req, err := f.request(query, "backfill")
			if err != nil {
				return err
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			if !a.jsonFlag {
				fmt.Printf("Backfilling “%s” (up to %d pages per source) …\n", query, f.pages)
			}
			res, err := a.scan(cmd.Context(), c, req)
			if err != nil {
				return err
			}
			if a.jsonFlag {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				printScanProblems(res)
				for _, src := range scannedSources(res) {
					done := ""
					if res.BackfillComplete[src] {
						done = " · history complete"
					}
					fmt.Printf("  %s: %d matched from %d page(s)%s\n", src, res.BySource[src], res.PagesBySource[src], done)
				}
				fmt.Printf("✓ %d fetched · %d historical mentions added\n", res.Fetched, res.New)
			}
			if len(res.Errors) > 0 && len(res.Errors) == len(scannedSources(res)) {
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
			req, err := f.request(query, "incremental")
			if err != nil {
				return err
			}
			if every < 30 {
				return errors.New("--every must be at least 30 seconds")
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			fmt.Printf("Watching “%s” every %ds on %s (Ctrl-C to stop)\n", query, every, c.Server)
			for n := 1; ; n++ {
				res, err := a.scan(ctx, c, req)
				switch {
				case ctx.Err() != nil:
					fmt.Println("\nStopped watching.")
					return nil
				case errors.Is(err, client.ErrNotSignedIn):
					return err
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
	r, err := server.BuildReport(st, store.Scope{Query: query})
	if err != nil {
		return err
	}
	fmt.Printf("\n“%s” — %d mentions\n", query, r.Summary.Total)
	printReportBody(r)
	return nil
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
