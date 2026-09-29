package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/config"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/sources"
	"github.com/verrloren/radaro/internal/store"
	"github.com/verrloren/radaro/skills"
)

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "What is set up: database, sources, accounts, keywords and drafts",
		Args:  cobra.NoArgs,
		RunE: a.withStore(func(st *store.Store, _ []string) error {
			accounts, err := st.Accounts("")
			if err != nil {
				return err
			}
			queries, err := st.Queries(0)
			if err != nil {
				return err
			}
			drafts, err := st.DraftCounts()
			if err != nil {
				return err
			}
			srcOpts, err := pipeline.SourceOptions(a.cfg, st)
			if err != nil {
				return err
			}
			var srcs []sourceStatus
			for _, info := range sources.All() {
				srcs = append(srcs, sourceStatus{info.Name, sources.Configured(info.Name, srcOpts)})
			}
			status := map[string]any{
				"version":         version,
				"database":        st.Path(),
				"data_dir":        config.DataDir(),
				"default_sources": a.cfg.Sources,
				"sources":         srcs,
				"accounts":        accounts,
				"keywords":        queries,
				"drafts":          drafts,
			}
			if a.jsonFlag {
				return printJSON(status)
			}
			fmt.Printf("radaro %s\n", version)
			fmt.Printf("database   %s\n", st.Path())
			fmt.Printf("sources    %s (default: %s)\n", configuredSources(srcs), strings.Join(a.cfg.Sources, ", "))
			if len(accounts) == 0 {
				fmt.Println("accounts   none — connect with: radaro connect bluesky|mastodon|devto|reddit")
			}
			for _, acc := range accounts {
				fmt.Printf("account    #%d %s %s\n", acc.ID, acc.Platform, acc.Handle)
			}
			fmt.Printf("keywords   %d tracked\n", len(queries))
			fmt.Printf("drafts     %s\n", countsLine(drafts))
			return nil
		}),
	}
}

type sourceStatus struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

func configuredSources(list []sourceStatus) string {
	var names []string
	for _, s := range list {
		if s.Configured {
			names = append(names, s.Name)
		}
	}
	return strings.Join(names, ", ")
}

func (a *app) skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Teach your coding agent (Claude Code, Codex, …) to use Radaro",
	}
	var agents []string
	var dir string
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the radaro skill into Claude Code and/or Codex",
		Long: "Copies the bundled skill (SKILL.md + references) to\n" +
			"  claude: ~/.claude/skills/radaro\n" +
			"  codex:  $CODEX_HOME/skills/radaro (default ~/.codex/skills/radaro)\n" +
			"By default it installs for every agent whose config directory exists. Use --dir for any other\n" +
			"Agent Skills-compatible tool.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			targets, err := skillTargets(agents, dir)
			if err != nil {
				return err
			}
			for _, t := range targets {
				if err := writeSkill(t); err != nil {
					return err
				}
				fmt.Printf("✓ installed the radaro skill to %s\n", t)
			}
			fmt.Println("Ask your agent something like: “promote my project <link> with radaro”.")
			return nil
		},
	}
	install.Flags().StringSliceVar(&agents, "agent", nil, "claude, codex (default: every agent found)")
	install.Flags().StringVar(&dir, "dir", "", "install into <dir>/radaro instead")

	show := &cobra.Command{
		Use: "show", Short: "Print the bundled SKILL.md", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			b, err := fs.ReadFile(skills.Radaro(), "SKILL.md")
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(b)
			return err
		},
	}
	cmd.AddCommand(install, show)
	return cmd
}

func skillTargets(agents []string, dir string) ([]string, error) {
	if dir != "" {
		return []string{filepath.Join(dir, "radaro")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	roots := map[string]string{"claude": filepath.Join(home, ".claude"), "codex": codexHome}
	explicit := len(agents) > 0
	if !explicit {
		agents = []string{"claude", "codex"}
	}
	var out []string
	for _, ag := range agents {
		root, ok := roots[strings.ToLower(strings.TrimSpace(ag))]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (use claude, codex, or --dir)", ag)
		}
		if _, err := os.Stat(root); err != nil && !explicit {
			continue // that agent is not installed
		}
		out = append(out, filepath.Join(root, "skills", "radaro"))
	}
	if len(out) == 0 {
		return nil, errors.New("no Claude Code (~/.claude) or Codex (~/.codex) found; pass --agent or --dir")
	}
	return out, nil
}

func writeSkill(target string) error {
	src := skills.Radaro()
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(target, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
