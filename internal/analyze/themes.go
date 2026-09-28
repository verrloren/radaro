package analyze

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/verrloren/radaro/internal/model"
)

// Stopwords, including generic product/review and valence words that make poor
// topic labels. Adapted from Harken (MIT) and extended.
var stopwords = setOf(
	"the", "a", "an", "and", "or", "but", "if", "then", "is", "are", "was", "were", "be",
	"been", "being", "to", "of", "in", "on", "for", "with", "at", "by", "from", "as", "into",
	"about", "it", "its", "this", "that", "these", "those", "i", "you", "he", "she", "we",
	"they", "them", "my", "your", "our", "their", "me", "us", "so", "just", "very", "really",
	"not", "no", "yes", "can", "will", "would", "should", "could", "do", "does", "did",
	"have", "has", "had", "get", "got", "im", "ive", "id", "youre", "dont", "doesnt", "didnt",
	"isnt", "wasnt", "arent", "thats", "whats", "there", "here", "what", "which", "who",
	"when", "where", "why", "how", "all", "any", "some", "more", "most", "much", "many",
	"one", "two", "up", "out", "down", "over", "than", "too", "also", "like", "use", "using",
	"used", "still", "even", "now", "new", "make", "makes", "made", "take", "takes", "taking",
	"via", "after", "before", "while", "well", "because", "http", "https", "com", "www", "amp",
	"app", "apps", "product", "products", "tool", "tools", "thing", "things", "both",
	"around", "actually", "again", "genuinely", "honestly", "feel", "feels", "whole", "bit",
	"nothing", "groundbreaking", "note", "notes", "good", "better", "bad", "great", "love",
	"loved", "nice", "best", "worse", "beautiful", "delightful", "confusing", "expensive",
	"ridiculous", "solid", "fine",
	// generic verbs and fillers that dominate forum threads without naming a topic
	"want", "wants", "wanted", "try", "trying", "tried", "run", "runs", "running", "getting",
	"need", "needs", "know", "think", "going", "way", "something", "anyone", "someone",
)

// A conservative set of high-confidence variants collapsed before counting.
var aliases = map[string]string{
	"cost": "pricing", "costs": "pricing", "price": "pricing", "prices": "pricing",
	"docs": "documentation",
	"fast": "performance", "faster": "performance", "fastest": "performance",
	"latency": "performance", "performant": "performance", "slow": "performance",
	"sluggish": "performance", "speed": "performance", "speedy": "performance",
	"synchronization": "sync", "syncing": "sync",
}

var (
	themeTokenRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9'+-]+`)
	openSourceRE = regexp.MustCompile(`\b(?:open|closed)\s+source\b`)
	digitsRE     = regexp.MustCompile(`^[0-9]+$`)
)

// Theme is a cluster of mentions sharing a salient term.
type Theme struct {
	Label      string   `json:"label"`
	Terms      []string `json:"terms"`
	Count      int      `json:"count"`
	MentionIDs []string `json:"-"`
}

// ThemeExtractor clusters mentions by their dominant shared terms. No key, no
// embeddings: a transparent document-frequency extractor.
type ThemeExtractor struct {
	MaxThemes  int
	MinCluster int
}

// NewThemeExtractor returns the default extractor (≤6 themes, ≥2 mentions each).
func NewThemeExtractor() ThemeExtractor { return ThemeExtractor{MaxThemes: 6, MinCluster: 2} }

func themeTokens(text string, extraStop map[string]bool) []string {
	var out []string
	normalized := openSourceRE.ReplaceAllString(strings.ToLower(text), "open-source")
	for _, t := range themeTokenRE.FindAllString(normalized, -1) {
		t = strings.Trim(strings.SplitN(t, "'", 2)[0], "-+") // quill's -> quill
		if len(t) < 3 || stopwords[t] || extraStop[t] || digitsRE.MatchString(t) {
			continue
		}
		if a, ok := aliases[t]; ok {
			t = a
		}
		out = append(out, t)
	}
	return out
}

type termCount struct {
	term  string
	count int
}

// ranked orders counts by count desc, then term asc, for deterministic labels.
func ranked(counts map[string]int) []termCount {
	out := make([]termCount, 0, len(counts))
	for t, c := range counts {
		out = append(out, termCount{t, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].term < out[j].term
	})
	return out
}

// Extract assigns each clustered mention its theme label in place (clearing
// stale labels) and returns the themes, largest first.
func (e ThemeExtractor) Extract(mentions []*model.Mention) []Theme {
	if len(mentions) == 0 {
		return nil
	}
	for _, m := range mentions {
		m.Theme = nil
	}
	// the tracked query terms should not themselves become themes
	extraStop := map[string]bool{}
	for _, m := range mentions {
		for _, w := range themeTokenRE.FindAllString(strings.ToLower(m.Query), -1) {
			extraStop[w] = true
		}
	}

	df := map[string]int{}
	perMention := make([]map[string]bool, len(mentions))
	for i, m := range mentions {
		toks := map[string]bool{}
		for _, t := range themeTokens(m.Content(), extraStop) {
			toks[t] = true
		}
		perMention[i] = toks
		for t := range toks {
			df[t]++
		}
	}

	var themes []Theme
	claimed := make([]bool, len(mentions))
	for _, seed := range ranked(df) {
		if seed.count < e.MinCluster {
			break
		}
		if len(themes) >= e.MaxThemes {
			break
		}
		var members []int
		for i := range mentions {
			if !claimed[i] && perMention[i][seed.term] {
				members = append(members, i)
			}
		}
		if len(members) < e.MinCluster {
			continue
		}
		// A label's second word should describe the cluster: keep it only when
		// it recurs in at least half the cluster (and at least two mentions).
		co := map[string]int{}
		for _, i := range members {
			for t := range perMention[i] {
				if t != seed.term {
					co[t]++
				}
			}
		}
		terms := []string{seed.term}
		need := max(2, int(math.Ceil(float64(len(members))/2)))
		for k, tc := range ranked(co) {
			if k >= 2 {
				break
			}
			if tc.count >= need {
				terms = append(terms, tc.term)
			}
		}
		label := seed.term
		if len(terms) > 1 {
			label = terms[0] + " / " + terms[1]
		}
		th := Theme{Label: label, Terms: terms, Count: len(members)}
		for _, i := range members {
			l := label
			mentions[i].Theme = &l
			claimed[i] = true
			th.MentionIDs = append(th.MentionIDs, mentions[i].ID)
		}
		themes = append(themes, th)
	}
	sort.SliceStable(themes, func(i, j int) bool {
		if themes[i].Count != themes[j].Count {
			return themes[i].Count > themes[j].Count
		}
		return themes[i].Label < themes[j].Label
	})
	return themes
}
