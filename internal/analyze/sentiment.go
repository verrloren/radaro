// Package analyze holds the local, key-free analyzers: a lexicon sentiment
// scorer and a term-frequency theme extractor.
package analyze

import (
	"math"
	"regexp"
	"strings"

	"github.com/verrloren/radaro/internal/model"
)

// Values are signed valence in roughly [-3, 3]. See NOTICE for license details.
var lexicon = map[string]float64{
	// strong positive
	"amazing": 3, "awesome": 3, "excellent": 3, "fantastic": 3, "incredible": 3,
	"love": 3, "loved": 3, "perfect": 3, "perfectly": 3, "brilliant": 3,
	"outstanding": 3, "wonderful": 3, "superb": 3, "delightful": 3, "phenomenal": 3, "gem": 3,
	// positive
	"good": 2, "great": 2, "nice": 2, "helpful": 2, "useful": 2, "solid": 2, "clean": 2,
	"cleaner": 2, "beautiful": 2, "gorgeous": 2, "fast": 2, "instant": 2, "instantly": 2,
	"smooth": 2, "happy": 2, "glad": 2, "impressed": 2, "impressive": 2, "reliable": 2,
	"intuitive": 2, "elegant": 2, "polished": 2, "recommend": 2, "recommended": 2,
	"works": 1.5, "working": 1.5, "worth": 1.5, "trust": 1.5, "thanks": 2, "thank": 2,
	"appreciate": 2, "win": 2, "winning": 2, "best": 2.5, "like": 1.5, "liked": 1.5,
	"enjoy": 2, "enjoyed": 2, "cool": 1.5, "neat": 1.5, "yay": 2, "kudos": 2,
	"promising": 1.5, "slick": 2, "lightweight": 1.5, "free": 1,
	// mild positive
	"ok": 0.5, "okay": 0.5, "fine": 0.5, "decent": 1, "better": 1, "improved": 1.5, "fixed": 2,
	// negative
	"bad": -2, "poor": -2, "slow": -2, "buggy": -2.5, "bug": -1.5, "broken": -2.5, "broke": -2,
	"crash": -2.5, "crashes": -2.5, "crashed": -2.5, "crashing": -2.5, "corrupt": -3,
	"corrupted": -3, "fail": -2, "failed": -2, "failing": -2, "error": -1.5, "errors": -1.5,
	"issue": -1, "issues": -1, "problem": -1.5, "problems": -1.5, "complaint": -1.5,
	"complaints": -1.5, "annoying": -2, "frustrating": -2.5, "frustrated": -2.5,
	"confusing": -2, "confused": -1.5, "disappointing": -2.5, "disappointed": -2.5,
	"useless": -3, "worthless": -3, "garbage": -3, "trash": -3, "horrible": -3,
	"terrible": -3, "awful": -3, "hate": -3, "hated": -3, "worst": -3, "sucks": -2.5,
	"suck": -2.5, "painful": -2, "clunky": -2, "bloated": -2, "expensive": -1.5,
	"overpriced": -2, "scam": -3, "spam": -2, "lacking": -1.5, "missing": -1,
	"unreliable": -2.5, "insecure": -2, "vulnerable": -1.5, "regret": -2.5, "meh": -1,
	"disaster": -3, "nightmare": -3, "concerned": -1.5, "concern": -1.5, "worried": -1.5,
	"ugly": -2, "worse": -2, "rough": -1.5, "steep": -2.5, "ridiculous": -2.5,
	"dealbreaker": -2.5,
	// churn / brand-monitoring signals: unresponsive support, outages, price hikes
	"outage": -2, "outages": -2, "downtime": -2, "unresponsive": -2.5, "ghosted": -2.5,
	"churn": -1.5, "churned": -2, "overhyped": -2, "hike": -1.5, "hiked": -1.5,
}

// Fixed idioms whose sentiment doesn't decompose into single tokens, matched
// as lowercase substrings of the raw text. Ordered for deterministic scoring.
var phraseValence = []struct {
	phrase string
	value  float64
}{
	{"no notice", -2.0},
	{"never replied", -2.0},
	{"never responded", -2.0},
	{"no response", -1.5},
	{"switched off", -1.5},
	{"went silent", -2.0},
	{"radio silence", -2.0},
	{"lack of", -1.5},
	{"non-starter", -2.5},
	{"pricing went up", -2.0},
	{"price went up", -2.0},
	{"too much for me", -2.0},
	// Stock phrases that are conventionally neutral despite positive tokens.
	{"works as described", -1.5},
	{"nothing special", -0.5},
}

var intensifiers = map[string]float64{
	"very": 1.4, "really": 1.4, "absolutely": 1.6, "extremely": 1.7, "so": 1.3,
	"super": 1.5, "incredibly": 1.6, "totally": 1.4, "completely": 1.4, "highly": 1.4,
	"insanely": 1.6, "ridiculously": 1.5, "remarkably": 1.4,
}

var dampeners = map[string]float64{
	"slightly": 0.6, "somewhat": 0.7, "kinda": 0.7, "barely": 0.5, "a": 1.0, "bit": 0.7, "little": 0.7,
}

var negators = setOf(
	"not", "no", "never", "n't", "cannot", "cant", "can't", "without", "hardly", "neither",
	"nor", "isnt", "isn't", "wasnt", "wasn't", "dont", "don't", "doesnt", "doesn't", "didnt",
	"didn't", "less", "wont", "won't", "wouldnt", "wouldn't", "couldnt", "couldn't",
	"shouldnt", "shouldn't", "arent", "aren't", "werent", "weren't", "havent", "haven't",
	"hasnt", "hasn't", "hadnt", "hadn't", "aint", "ain't",
)

// Contrastive-conjunction weighting: sentiment before "but" matters less than
// what follows it ("fine but overhyped" should read negative).
const (
	contrastPre  = 0.5
	contrastPost = 1.5
)

var (
	positiveEmoji = runeSet("🎉🚀😀😃😄😁😊🙂👍❤💜✨🔥💯🥳😍🤩👏")
	negativeEmoji = runeSet("😞😢😭😠😡👎💩😤🤬😩😖💔🙄😒")
	quoteReplacer = strings.NewReplacer("’", "'", "‘", "'", "ʼ", "'")
	sentTokenRE   = regexp.MustCompile(`[a-zA-Z']+`)
)

// SentimentResult is a label plus a signed score in roughly [-1, 1].
type SentimentResult struct {
	Label model.Sentiment
	Score float64
}

// Lexicon is a rule-based sentiment scorer: a curated valence lexicon plus
// negation, intensifier and contrastive-conjunction handling, scored
// VADER-style. Stateless and safe for concurrent use.
type Lexicon struct {
	Threshold float64
}

// NewLexicon returns the scorer with the default ±0.15 neutral band.
func NewLexicon() Lexicon { return Lexicon{Threshold: 0.15} }

// Score labels text.
func (l Lexicon) Score(text string) SentimentResult {
	if strings.TrimSpace(text) == "" {
		return SentimentResult{model.Neutral, 0}
	}
	text = quoteReplacer.Replace(text)
	lowered := strings.ToLower(text)
	tokens := sentTokenRE.FindAllString(lowered, -1)
	butIdx := -1
	for i, t := range tokens {
		if t == "but" {
			butIdx = i
			break
		}
	}

	total := 0.0
	hits, positiveHits, negativeHits := 0, 0, 0
	for i, tok := range tokens {
		val, ok := lexicon[tok]
		if !ok {
			continue
		}
		hits++
		if val > 0 {
			positiveHits++
		} else if val < 0 {
			negativeHits++
		}
		// look back up to 3 tokens for negators / intensifiers
		mult, negated := 1.0, false
		for _, prev := range tokens[max(0, i-3):i] {
			if isNegator(prev) {
				negated = true
			}
			if m, ok := intensifiers[prev]; ok {
				mult *= m
			}
			if m, ok := dampeners[prev]; ok {
				mult *= m
			}
		}
		v := val * mult
		if negated {
			v = -v * 0.85 // negation flips and slightly dampens
		}
		if butIdx >= 0 {
			if i < butIdx {
				v *= contrastPre
			} else {
				v *= contrastPost
			}
		}
		total += v
	}

	// A negator directly before an idiom ("no lack of") inverts it, so the
	// penalty is suppressed; a negator in a neighbouring clause does not.
	for _, p := range phraseValence {
		idx := strings.Index(lowered, p.phrase)
		if idx == -1 {
			continue
		}
		before := lowered[:idx]
		preceding := sentTokenRE.FindAllString(before, -1)
		if len(preceding) > 0 {
			prev := preceding[len(preceding)-1]
			if strings.HasSuffix(strings.TrimRight(before, " \t\r\n"), prev) && isNegator(prev) {
				continue
			}
		}
		total += p.value
		hits++
	}

	// Explicitly distributed opinions describe a mixed population rather than
	// one author's strong stance ("some users like it, others hate it").
	if contains(tokens, "some") && contains(tokens, "others") && positiveHits > 0 && negativeHits > 0 {
		total *= 0.2
	}

	for _, r := range text {
		if positiveEmoji[r] {
			total += 2
			hits++
		} else if negativeEmoji[r] {
			total -= 2
			hits++
		}
	}

	if hits == 0 {
		return SentimentResult{model.Neutral, 0}
	}
	// VADER-style normalisation: squash the unbounded sum into (-1, 1).
	norm := total / math.Sqrt(total*total+4)
	label := model.Neutral
	if norm >= l.Threshold {
		label = model.Positive
	} else if norm <= -l.Threshold {
		label = model.Negative
	}
	return SentimentResult{label, math.Round(norm*1e4) / 1e4}
}

func isNegator(tok string) bool {
	return negators[tok] || strings.HasSuffix(tok, "n't")
}

func contains(tokens []string, want string) bool {
	for _, t := range tokens {
		if t == want {
			return true
		}
	}
	return false
}

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// runeSet ignores variation selectors so "❤️" matches on its base code point once.
func runeSet(chars string) map[rune]bool {
	m := map[rune]bool{}
	for _, r := range chars {
		if r != '︎' && r != '️' {
			m[r] = true
		}
	}
	return m
}
