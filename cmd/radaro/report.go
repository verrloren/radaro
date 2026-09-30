package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/client"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/sources"
)

// printReport prints the server's report for a keyword.
func (a *app) printReport(ctx context.Context, c *client.Client, query string) error {
	var r server.Report
	if err := c.Do(ctx, "GET", "/api/report", url.Values{"q": {query}}, nil, &r); err != nil {
		return err
	}
	fmt.Printf("\n“%s” — %d mentions\n", query, r.Summary.Total)
	printReportBody(&r)
	return nil
}

func printReportBody(r *server.Report) {
	total := max(r.Summary.Total, 1)
	for _, s := range []string{"positive", "neutral", "negative"} {
		n := r.Summary.BySentiment[s]
		bar := strings.Repeat("█", int(math.Round(20*float64(n)/float64(total))))
		fmt.Printf("  %8s %s %d\n", s, bar, n)
	}
	fmt.Printf("  %8s %+.0f%%\n", "net", r.Net*100)
	fmt.Printf("  %8s %s\n", "sources", countsLine(r.Summary.BySource))
	if len(r.Themes) > 0 {
		fmt.Println("\n  Top themes")
		for _, t := range r.Themes {
			fmt.Printf("    %-28s %d\n", t.Label, t.Count)
		}
	}
	if len(r.TopPositive)+len(r.TopNegative) > 0 {
		fmt.Println()
		for _, m := range r.TopPositive {
			fmt.Printf("  + [%s] %s\n", m.Source, clip(m.Content(), 120))
		}
		for _, m := range r.TopNegative {
			fmt.Printf("  − [%s] %s\n", m.Source, clip(m.Content(), 120))
		}
	}
}

func countsLine(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s (%d)", k, counts[k])
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (a *app) reportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report [keyword]",
		Short: "Print a sentiment + theme report for a keyword (default: most recent)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			var queries []string
			if err := c.Do(cmd.Context(), "GET", "/api/queries", nil, nil, &queries); err != nil {
				return err
			}
			q := ""
			if len(args) == 1 {
				q = strings.TrimSpace(args[0])
			} else if len(queries) > 0 {
				q = queries[0]
			}
			if q == "" {
				return errors.New(`no data yet — run radaro track "keyword"`)
			}
			if !containsString(queries, q) {
				return fmt.Errorf("no data found for “%s”", q)
			}
			if a.jsonFlag {
				var r server.Report
				if err := c.Do(cmd.Context(), "GET", "/api/report", url.Values{"q": {q}}, nil, &r); err != nil {
					return err
				}
				return printJSON(r)
			}
			return a.printReport(cmd.Context(), c, q)
		},
	}
}

func (a *app) sourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sources",
		Short: "List available mention sources and which are set up on the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.api()
			if err != nil {
				return err
			}
			type row struct {
				sources.Info
				Configured bool `json:"configured"`
			}
			var meta struct {
				Sources []row `json:"sources"`
			}
			if err := c.Do(cmd.Context(), "GET", "/api/meta", nil, nil, &meta); err != nil {
				return err
			}
			if a.jsonFlag {
				return printJSON(meta.Sources)
			}
			fmt.Printf("%-15s %-16s %s\n", "NAME", "LABEL", "STATUS")
			for _, r := range meta.Sources {
				status := "zero-config"
				if r.NeedsConfig {
					status = "needs setup"
					if r.Configured {
						status = "configured"
					}
				}
				fmt.Printf("%-15s %-16s %s\n", r.Name, r.Label, status)
			}
			return nil
		},
	}
}

func (a *app) exportCmd() *cobra.Command {
	var format, output string
	var project int64
	cmd := &cobra.Command{
		Use:   "export [keyword]",
		Short: "Export complete mention records as JSON or CSV (default: all your keywords)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format = strings.ToLower(strings.TrimSpace(format))
			if format != "json" && format != "csv" {
				return errors.New("--format must be json or csv")
			}
			c, err := a.api()
			if err != nil {
				return err
			}
			q := url.Values{}
			if len(args) == 1 {
				q.Set("q", strings.TrimSpace(args[0]))
			}
			if project != 0 {
				q.Set("p", strconv.FormatInt(project, 10))
			}
			var rows []*model.Mention
			if err := c.Do(cmd.Context(), "GET", "/api/export", q, nil, &rows); err != nil {
				return err
			}
			if len(rows) == 0 {
				return errors.New("no data to export")
			}
			var w io.Writer = os.Stdout
			if output != "-" {
				if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
					return err
				}
				f, err := os.Create(output)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			if err := writeExport(w, rows, format); err != nil {
				return err
			}
			if output != "-" {
				stderr("✓ exported %d mentions to %s\n", len(rows), output)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "json", "output format: json or csv")
	cmd.Flags().StringVarP(&output, "output", "o", "-", "output file, or - for stdout")
	cmd.Flags().Int64Var(&project, "project", 0, "only this project's keywords")
	return cmd
}

func writeExport(w io.Writer, rows []*model.Mention, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "source", "query", "author", "title", "text", "url", "created_at", "score", "sentiment", "sentiment_score", "theme"})
	for _, m := range rows {
		score, sentScore := "", ""
		if m.Score != nil {
			score = strconv.FormatInt(*m.Score, 10)
		}
		if m.SentimentScore != nil {
			sentScore = strconv.FormatFloat(*m.SentimentScore, 'f', -1, 64)
		}
		_ = cw.Write([]string{m.ID, m.Source, m.Query, deref(m.Author), deref(m.Title), m.Text, deref(m.URL),
			m.CreatedAt.Format(time.RFC3339), score, string(m.Sentiment), sentScore, deref(m.Theme)})
	}
	cw.Flush()
	return cw.Error()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
