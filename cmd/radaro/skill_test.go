package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/skills"
)

// The skill is the contract with users' agents: every command and flag it
// mentions must exist, or agents will call things that fail.
func TestSkillMatchesCLI(t *testing.T) {
	root := newRoot()
	cmdRE := regexp.MustCompile("(?m)(?:^|[\\s`(])radaro ((?:[a-z][a-z-]* ?)+)((?:[^`\\n|]*))")
	flagRE := regexp.MustCompile(`--([a-z][a-z-]*)`)
	checked := 0
	err := fs.WalkDir(skills.Radaro(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(skills.Radaro(), path)
		for _, m := range cmdRE.FindAllStringSubmatch(string(b), -1) {
			words := strings.Fields(m[1])
			if len(words) == 0 || words[0] == "skill" && len(words) == 1 {
				continue
			}
			cmd, rest, err := root.Find(words)
			if err != nil || cmd == root {
				t.Errorf("%s: unknown command %q", path, "radaro "+m[1])
				continue
			}
			if len(rest) > 0 && !cmd.Runnable() {
				t.Errorf("%s: %q is not a subcommand of %q", path, rest[0], cmd.CommandPath())
			}
			for _, f := range flagRE.FindAllStringSubmatch(m[2], -1) {
				if !hasFlag(cmd, f[1]) {
					t.Errorf("%s: %q has no flag --%s", path, cmd.CommandPath(), f[1])
				}
			}
			checked++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("only %d command references found; the regexp is probably broken", checked)
	}
}

func hasFlag(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil ||
		cmd.Root().PersistentFlags().Lookup(name) != nil
}

func TestSkillFrontmatter(t *testing.T) {
	b, err := fs.ReadFile(skills.Radaro(), "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(b), "---", 3)
	if len(parts) < 3 || strings.TrimSpace(parts[0]) != "" {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	var name, desc string
	for _, line := range strings.Split(parts[1], "\n") {
		if v, ok := strings.CutPrefix(line, "name: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(line, "description: "); ok {
			desc = v
		}
	}
	if name != "radaro" {
		t.Fatalf("name = %q", name)
	}
	// The Agent Skills spec caps the description at 1024 characters.
	if desc == "" || len(desc) > 1024 {
		t.Fatalf("description length %d", len(desc))
	}
	for _, ref := range regexp.MustCompile(`\]\((references/[^)#]+)`).FindAllStringSubmatch(parts[2], -1) {
		if _, err := fs.Stat(skills.Radaro(), ref[1]); err != nil {
			t.Errorf("SKILL.md links to missing %s", ref[1])
		}
	}
}

func TestSkillInstallTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if _, err := skillTargets(nil, ""); err == nil {
		t.Fatal("no agents installed should be an error")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := skillTargets(nil, "")
	if err != nil || len(got) != 1 || got[0] != filepath.Join(home, ".codex", "skills", "radaro") {
		t.Fatalf("targets %v %v", got, err)
	}
	if err := writeSkill(got[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got[0], "references", "platforms.md")); err != nil {
		t.Fatal("references not installed")
	}
	if _, err := skillTargets([]string{"vim"}, ""); err == nil {
		t.Fatal("unknown agent accepted")
	}
}
