package analyze

import (
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

func TestLexiconLabels(t *testing.T) {
	lex := NewLexicon()
	cases := []struct {
		text string
		want model.Sentiment
	}{
		{"This is amazing, I love it", model.Positive},
		{"Terrible, it crashes constantly and the sync is broken", model.Negative},
		{"", model.Neutral},
		{"The release ships on Tuesday.", model.Neutral},
		{"not good at all", model.Negative},
		{"I don’t hate it", model.Positive}, // curly apostrophe still negates
		{"The docs are fine but the pricing is ridiculous", model.Negative},
		{"Support went silent after the outage", model.Negative},
		{"There is no lack of features", model.Neutral},
		{"shipped it 🚀🎉", model.Positive},
		{"👎", model.Negative},
	}
	for _, c := range cases {
		if got := lex.Score(c.text).Label; got != c.want {
			t.Errorf("Score(%q) = %s, want %s", c.text, got, c.want)
		}
	}
}

func TestLexiconScoreBounded(t *testing.T) {
	r := NewLexicon().Score("amazing amazing amazing awesome excellent perfect love")
	if r.Score <= 0.9 || r.Score >= 1 {
		t.Fatalf("score %v not squashed into (0.9, 1)", r.Score)
	}
}

func mention(id, query, text string) *model.Mention {
	return &model.Mention{ID: id, Source: "hackernews", Query: query, Text: text, CreatedAt: time.Now()}
}

func TestThemeExtraction(t *testing.T) {
	stale := "old label"
	ms := []*model.Mention{
		mention("1", "Kestrel", "Kestrel pricing is too high"),
		mention("2", "Kestrel", "The price of Kestrel went up again"),
		mention("3", "Kestrel", "Kestrel sync lost my notes"),
		mention("4", "Kestrel", "syncing broke, sync is unreliable"),
		mention("5", "Kestrel", "Unrelated remark"),
	}
	ms[4].Theme = &stale

	themes := NewThemeExtractor().Extract(ms)
	if len(themes) != 2 {
		t.Fatalf("got %d themes, want 2: %+v", len(themes), themes)
	}
	labels := map[string]int{}
	for _, th := range themes {
		labels[th.Label] = th.Count
	}
	if labels["pricing"] != 2 || labels["sync"] != 2 {
		t.Fatalf("unexpected themes %v", labels)
	}
	if ms[4].Theme != nil {
		t.Fatalf("stale theme not cleared: %q", *ms[4].Theme)
	}
	for _, th := range themes {
		if th.Label == "kestrel" {
			t.Fatal("the tracked query itself became a theme")
		}
	}
}

func TestThemeExtractionDeterministic(t *testing.T) {
	build := func() []*model.Mention {
		return []*model.Mention{
			mention("1", "x", "alpha beta gamma"),
			mention("2", "x", "alpha beta delta"),
			mention("3", "x", "gamma delta alpha"),
		}
	}
	first := NewThemeExtractor().Extract(build())
	for range 20 {
		again := NewThemeExtractor().Extract(build())
		if len(again) != len(first) || again[0].Label != first[0].Label {
			t.Fatalf("labels changed between runs: %v vs %v", first, again)
		}
	}
}
