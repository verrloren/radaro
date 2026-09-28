// Package sampledata is the bundled dataset for `radaro demo`.
//
// Every mention is SYNTHETIC: a fictional product ("Kestrel", a local-first
// notes app) discussed by fictional accounts. It exists so the demo shows the
// full pipeline with no network and no keys.
package sampledata

import (
	"fmt"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// Query is the keyword the demo dataset is tracked under.
const Query = "Kestrel"

type raw struct {
	source, author, title, text string
	score                       int64
	daysAgo                     int
}

var rows = []raw{
	{"hackernews", "lowtide", "Show HN: Kestrel – a local-first notes app that opens instantly", "", 388, 13},
	{"hackernews", "copperkettle", "", "Been using Kestrel for a month. Performance is impressive, opens instantly even with 6000 notes.", 91, 12},
	{"hackernews", "quietmoth", "", "The pricing jumped to $11/mo which feels steep for a notes app. Otherwise solid.", 58, 12},
	{"hackernews", "sable_io", "", "Kestrel's markdown export is broken for nested lists. Reported it twice, no response.", 37, 11},
	{"hackernews", "fjordbyte", "", "I love that Kestrel stores everything as plain files. No lock-in, works with my git workflow.", 133, 10},
	{"hackernews", "pinecone42", "", "The sync is unreliable — lost a note yesterday after a conflict. Scary for a notes app.", 70, 9},
	{"reddit", "u/marginalia_fan", "Kestrel vs the big note apps?", "Switched to Kestrel last week. The editor is so much cleaner and it's way less bloated.", 144, 8},
	{"reddit", "u/threadbare", "", "Honestly Kestrel is just okay. Does the job but nothing groundbreaking. The documentation is thin.", 22, 8},
	{"reddit", "u/nightowl_dev", "", "Kestrel crashed three times today on a large vault. Performance falls apart past 10k notes.", 49, 7},
	{"reddit", "u/offgrid_notes", "", "Love that Kestrel is local-first and doesn't phone home. No telemetry, no account required.", 176, 7},
	{"reddit", "u/budgetbee", "Kestrel pricing is too high", "$11/mo is ridiculous when plain markdown editors are free. The app is nice but not that nice.", 297, 6},
	{"reddit", "u/keyboard_monk", "", "Kestrel's keyboard shortcuts are fantastic. Feels like it was built by people who actually take notes.", 81, 6},
	{"reddit", "u/teamlead_ana", "", "We rolled Kestrel out to the whole team. Setup was painful and the documentation is missing half the features.", 43, 5},
	{"reddit", "u/opensourcerer", "", "Wish Kestrel were open source. Great app but I don't trust a closed source notes app with my second brain.", 305, 4},
	{"mastodon", "wren@social.example", "", "Kestrel's writing experience is genuinely delightful. Typography and performance are top notch.", 33, 12},
	{"mastodon", "hollis@toot.example", "", "Kestrel's plugin API is surprisingly good. Wrote a custom exporter in an afternoon.", 49, 10},
	{"mastodon", "marlow@fedi.example", "", "Kestrel is closed source and that's a dealbreaker for me. Looking for an open source alternative.", 41, 9},
	{"mastodon", "juniper@notes.example", "", "The pricing model killed it for me. $11/mo for notes I could keep in plain markdown? No thanks.", 28, 5},
	{"mastodon", "ember@writers.example", "", "Kestrel has become my daily driver for writing. The distraction-free mode is beautiful and fast.", 36, 3},
	{"mastodon", "tamsin@campus.example", "", "Kestrel keeps crashing on my old laptop. Performance is rough on low-end hardware.", 17, 2},
	{"bluesky", "rowan.bsky.social", "", "Kestrel's search is instant and the editor is gorgeous. Worth trying if you take a lot of notes.", 74, 11},
	{"bluesky", "calloway.bsky.social", "", "Kestrel crashed and corrupted a note. Backups saved me but this shouldn't happen in a notes app.", 39, 9},
	{"bluesky", "linnea.bsky.social", "", "Kestrel pricing went up again. Loved it at launch, but $11/mo is too much for me now.", 45, 7},
	{"bluesky", "odette.bsky.social", "", "The Kestrel sync bit me again. Lost edits after switching devices. Be careful.", 30, 6},
	{"bluesky", "birchwood.bsky.social", "", "Kestrel's docs are seriously lacking. Spent an hour figuring out how templates work.", 21, 4},
	{"bluesky", "sorrel.bsky.social", "", "Kestrel is the best note app I've tried this year. Fast, clean, local-first. Highly recommend.", 62, 1},
	{"hackernews", "tallgrass", "", "Kestrel is fast but the lack of mobile sync reliability makes it a non-starter for my workflow.", 25, 3},
	{"hackernews", "gullwing", "", "Kestrel is the first notes app where search is actually fast. Sub-100ms across my whole vault.", 99, 2},
	{"reddit", "u/longtime_user", "", "Two years on Kestrel. Still the fastest, still local-first, still love it. The plugin ecosystem grew a lot.", 108, 2},
	{"reddit", "u/sync_skeptic", "", "Kestrel is fine but overhyped. The search is good, the sync is bad, the price is worse.", 55, 1},
}

// Mentions returns the demo dataset anchored to now.
func Mentions(now time.Time) []*model.Mention {
	out := make([]*model.Mention, 0, len(rows))
	for i, r := range rows {
		created := now.Add(-time.Duration(r.daysAgo)*24*time.Hour - time.Duration(i)*37*time.Minute)
		m := &model.Mention{
			Source:    r.source,
			Query:     Query,
			Author:    model.Str(r.author),
			Title:     model.Str(r.title),
			Text:      r.text,
			URL:       model.Str(fmt.Sprintf("https://example.com/radaro-demo/%s/%d", r.source, i+1)),
			CreatedAt: created,
			Score:     model.Int(r.score),
		}
		m.Normalize()
		out = append(out, m)
	}
	return out
}
