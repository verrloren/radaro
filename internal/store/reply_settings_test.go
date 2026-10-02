package store

import (
	"github.com/verrloren/radaro/internal/model"
	"testing"
	"time"
)

func TestRedditStatsSurviveReingestWithoutMetadata(t *testing.T) {
	st := openTest(t)
	m := &model.Mention{Source: "reddit", Query: "go", URL: model.Str("https://reddit.com/r/go/comments/abc"), CreatedAt: time.Now(), Reddit: &model.RedditStats{Community: "go", Comments: model.Int(0)}}
	m.Normalize()
	if _, err := st.Upsert([]*model.Mention{m}, false); err != nil {
		t.Fatal(err)
	}
	m.Reddit = nil
	if _, err := st.Upsert([]*model.Mention{m}, false); err != nil {
		t.Fatal(err)
	}
	ms, err := st.Mentions(MentionFilter{ID: m.ID})
	if err != nil || len(ms) != 1 || ms[0].Reddit == nil || ms[0].Reddit.Comments == nil || *ms[0].Reddit.Comments != 0 {
		t.Fatalf("lost metadata %v %+v", err, ms)
	}
}
