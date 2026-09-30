package accountimport

import (
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	rows, err := Parse("secret,platform,instance,handle\nkey-1,mastodon,example.social,ignored\nkey-2,bluesky,,alice.bsky.social\n")
	if err != nil || len(rows) != 2 {
		t.Fatalf("parse: %v, %d rows", err, len(rows))
	}
	if rows[0].Number != 2 || rows[0].Platform != "mastodon" || rows[0].Instance != "example.social" || rows[0].Secret != "key-1" {
		t.Fatalf("first row: %+v", rows[0])
	}
	if rows[1].Number != 3 || rows[1].Handle != "alice.bsky.social" || rows[1].Error != "" {
		t.Fatalf("second row: %+v", rows[1])
	}

	rows, err = Parse("devto,platform,key-3\nreddit,bob,secret\nbluesky,,key-4")
	if err != nil || len(rows) != 3 || rows[0].Error != "" || rows[0].Handle != "platform" || !strings.Contains(rows[1].Error, "Add another Reddit account") || rows[2].Error == "" {
		t.Fatalf("headerless rows: %+v, %v", rows, err)
	}
}

func TestParseJSONAndLimits(t *testing.T) {
	rows, err := Parse(`[{"platform":"devto","secret":"key"},42,{"platform":"unknown","secret":"secret"}]`)
	if err != nil || len(rows) != 3 || rows[0].Error != "" || rows[1].Number != 2 || rows[1].Error == "" || rows[2].Error != "unknown platform" {
		t.Fatalf("JSON rows: %+v, %v", rows, err)
	}
	for _, input := range []string{"", "[]", "platform,secret", `[`, "platform,secret\n" + strings.Repeat("devto,key\n", 501)} {
		if _, err := Parse(input); err == nil {
			t.Errorf("expected error for %q", input[:min(len(input), 40)])
		}
	}
}
