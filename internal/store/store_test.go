package store

import (
	"errors"
	"testing"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mk(query, url, text string, sent model.Sentiment, at time.Time) *model.Mention {
	m := &model.Mention{Source: "hackernews", Query: query, Text: text, URL: model.Str(url), CreatedAt: at, Sentiment: sent}
	m.Normalize()
	return m
}

func TestUpsertCountsNewAndPreservesTheme(t *testing.T) {
	st := openTest(t)
	now := time.Now()
	a := mk("go", "https://a", "good", model.Positive, now)
	b := mk("go", "https://b", "bad", model.Negative, now)
	theme := "perf"
	a.Theme = &theme
	if n, err := st.Upsert([]*model.Mention{a, b}, true); err != nil || n != 2 {
		t.Fatalf("first upsert = %d, %v", n, err)
	}
	fresh := mk("go", "https://a", "good", model.Positive, now) // no theme
	if n, err := st.Upsert([]*model.Mention{fresh}, false); err != nil || n != 0 {
		t.Fatalf("re-upsert = %d, %v", n, err)
	}
	got, err := st.Mentions(MentionFilter{Scope: Scope{Query: "go"}, Source: "hackernews"})
	if err != nil || len(got) != 2 {
		t.Fatalf("mentions = %d, %v", len(got), err)
	}
	for _, m := range got {
		if m.ID == a.ID && (m.Theme == nil || *m.Theme != "perf") {
			t.Fatal("theme was cleared by a pre-cluster upsert")
		}
	}
	// The same post can belong to two keywords.
	other := mk("golang", "https://a", "good", model.Positive, now)
	if n, _ := st.Upsert([]*model.Mention{other}, true); n != 1 {
		t.Fatal("same URL under another query should be a new row")
	}
}

func TestSummaryAndTimeseries(t *testing.T) {
	st := openTest(t)
	day1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	day2 := day1.Add(48 * time.Hour)
	_, err := st.Upsert([]*model.Mention{
		mk("go", "https://1", "", model.Positive, day1),
		mk("go", "https://2", "", model.Negative, day1),
		mk("go", "https://3", "", model.Positive, day2),
		mk("rust", "https://4", "", model.Neutral, day2),
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := st.Summary(Scope{Query: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 3 || sum.BySentiment["positive"] != 2 || sum.ByDay["2026-09-01"] != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if net := NetSentiment(sum); net != 0.333 {
		t.Fatalf("net = %v", net)
	}
	ts, err := st.Timeseries(Scope{})
	if err != nil || len(ts) != 2 || ts[1].Total != 2 || ts[1].Neutral != 1 {
		t.Fatalf("timeseries %+v, %v", ts, err)
	}
	if _, err := st.Summary(Scope{Query: "go", ProjectID: 1}); err == nil {
		t.Fatal("query and project together should fail")
	}
}

func TestProjects(t *testing.T) {
	st := openTest(t)
	if err := st.SaveTracking("go", []string{"HackerNews", "hackernews", "bluesky"}, 0); err != nil {
		t.Fatal(err)
	}
	tr, err := st.Tracking("go")
	if err != nil || len(tr.Sources) != 2 {
		t.Fatalf("tracking %+v, %v", tr, err)
	}
	p, err := st.CreateProject("  Linux   stuff ")
	if err != nil || p.Name != "Linux stuff" {
		t.Fatalf("create %+v, %v", p, err)
	}
	if _, err := st.CreateProject("linux STUFF"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if added, err := st.AddQueryToProject(p.ID, "go"); err != nil || !added {
		t.Fatalf("add = %v, %v", added, err)
	}
	if _, err := st.AddQueryToProject(p.ID, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown keyword error = %v", err)
	}
	qs, _ := st.Queries(p.ID)
	if len(qs) != 1 || qs[0] != "go" {
		t.Fatalf("project queries %v", qs)
	}
	if _, err := st.DeleteProject(DefaultProjectID); !errors.Is(err, ErrConflict) {
		t.Fatal("Default project must be protected")
	}
	if _, err := st.RemoveQueryFromProject(DefaultProjectID, "go"); !errors.Is(err, ErrConflict) {
		t.Fatal("keywords cannot leave Default")
	}
	if ok, err := st.DeleteProject(p.ID); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if tr, _ := st.Tracking("go"); tr == nil {
		t.Fatal("deleting a project must keep its keywords")
	}
}

func TestScanStateTransitions(t *testing.T) {
	st := openTest(t)
	m := mk("go", "https://1", "", model.Neutral, time.Now())
	// First recent scan seeds the backfill cursor.
	if err := st.RecordSourceSuccess("go", "hackernews", []*model.Mention{m}, false, "100", time.Time{}); err != nil {
		t.Fatal(err)
	}
	s, _ := st.SourceState("go", "hackernews")
	if s.BackfillCursor != "100" || s.IncrementalCursor != "" || s.NewestAt == nil || s.BackfillComplete {
		t.Fatalf("after first scan: %+v", s)
	}
	// Later incremental scan that fell behind keeps a forward cursor.
	since := time.Now().Add(-time.Hour)
	if err := st.RecordSourceSuccess("go", "hackernews", nil, false, "200", since); err != nil {
		t.Fatal(err)
	}
	s, _ = st.SourceState("go", "hackernews")
	if s.IncrementalCursor != "200" || s.IncrementalSince == "" || s.BackfillCursor != "100" {
		t.Fatalf("after incremental: %+v", s)
	}
	// Backfill to the end.
	if err := st.RecordSourceSuccess("go", "hackernews", nil, true, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	s, _ = st.SourceState("go", "hackernews")
	if !s.BackfillComplete {
		t.Fatalf("backfill should be complete: %+v", s)
	}
	if err := st.RecordSourceError("go", "hackernews", "HTTP 500"); err != nil {
		t.Fatal(err)
	}
	s, _ = st.SourceState("go", "hackernews")
	if s.LastError == nil || s.IncrementalCursor != "200" {
		t.Fatalf("error must not move cursors: %+v", s)
	}
}

func TestAlertOutbox(t *testing.T) {
	st := openTest(t)
	m := mk("go", "https://1", "awful", model.Negative, time.Now())
	if _, err := st.Upsert([]*model.Mention{m}, true); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.EnqueueAlerts([]*model.Mention{m, m}, "k"); n != 1 {
		t.Fatalf("enqueued %d, want 1", n)
	}
	pending, _ := st.PendingAlerts("go", "k", 10)
	if len(pending) != 1 {
		t.Fatal("expected one pending alert")
	}
	_ = st.MarkAlerts("go", []string{m.ID}, "k", errors.New("boom"))
	if n, _ := st.PendingAlertCount("go", "k"); n != 1 {
		t.Fatal("failed delivery must stay pending")
	}
	_ = st.MarkAlerts("go", []string{m.ID}, "k", nil)
	if n, _ := st.PendingAlertCount("go", "k"); n != 0 {
		t.Fatal("delivered alert still pending")
	}
	if n, _ := st.EnqueueAlerts([]*model.Mention{m}, "k"); n != 0 {
		t.Fatal("delivered alert re-queued")
	}
}

func TestThresholdEpisodes(t *testing.T) {
	st := openTest(t)
	now := time.Now()
	payload := map[string]any{"event": "radaro.volume_spike"}
	if err := st.ActivateThresholdAlert("go", "volume_spike", "k", "spike", payload, 24, now); err != nil {
		t.Fatal(err)
	}
	// Re-activating while active does not duplicate.
	_ = st.ActivateThresholdAlert("go", "volume_spike", "k", "spike 2", payload, 24, now)
	alerts, _ := st.PendingThresholdAlerts("go", "k")
	if len(alerts) != 1 || alerts[0].Text != "spike 2" {
		t.Fatalf("pending %+v", alerts)
	}
	_ = st.MarkThresholdAlert(alerts[0].ID, nil)
	_ = st.ClearThresholdAlert("go", "volume_spike", "k", now)
	// Within the cooldown a new crossing is suppressed…
	_ = st.ActivateThresholdAlert("go", "volume_spike", "k", "again", payload, 24, now.Add(time.Hour))
	if n, _ := st.ThresholdAlertPendingCount("go", "k"); n != 0 {
		t.Fatal("cooldown not respected")
	}
	// …and after it, re-armed.
	_ = st.ActivateThresholdAlert("go", "volume_spike", "k", "again", payload, 24, now.Add(25*time.Hour))
	if n, _ := st.ThresholdAlertPendingCount("go", "k"); n != 1 {
		t.Fatal("episode not re-armed after cooldown")
	}
}

func TestAlertMetrics(t *testing.T) {
	st := openTest(t)
	now := time.Now()
	var ms []*model.Mention
	for i := range 4 { // current window
		ms = append(ms, mk("go", "https://c"+string(rune('a'+i)), "", model.Negative, now.Add(-time.Hour)))
	}
	ms = append(ms, mk("go", "https://old", "", model.Positive, now.Add(-30*time.Hour)))
	if _, err := st.Upsert(ms, true); err != nil {
		t.Fatal(err)
	}
	m, err := st.AlertMetrics("go", now, 24, 7)
	if err != nil {
		t.Fatal(err)
	}
	if m.CurrentCount != 4 || m.BaselineCount != 1 || *m.CurrentNetSentiment != -1 || *m.BaselineNetSentiment != 1 {
		t.Fatalf("metrics %+v", m)
	}
}
