// Package store is Radaro's SQLite persistence. One file, no server; the data
// stays on the user's machine.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/verrloren/radaro/internal/model"
)

// DefaultProjectID is the protected project every keyword starts in.
const DefaultProjectID int64 = 1

// timeLayout is fixed-width UTC so stored timestamps sort lexically.
const timeLayout = "2006-01-02T15:04:05.000000Z"

const schemaVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS mentions (
    id              TEXT NOT NULL,
    source          TEXT NOT NULL,
    query           TEXT NOT NULL,
    author          TEXT,
    title           TEXT,
    text            TEXT,
    url             TEXT,
    created_at      TEXT NOT NULL,
    score           INTEGER,
    sentiment       TEXT,
    sentiment_score REAL,
    theme           TEXT,
    fetched_at      TEXT NOT NULL,
    PRIMARY KEY (id, query)
);
CREATE INDEX IF NOT EXISTS idx_mentions_query ON mentions(query);
CREATE INDEX IF NOT EXISTS idx_mentions_created ON mentions(created_at);

CREATE TABLE IF NOT EXISTS tracked_queries (
    query           TEXT PRIMARY KEY,
    sources         TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    last_scanned_at TEXT
);
CREATE TABLE IF NOT EXISTS source_scan_state (
    query              TEXT NOT NULL,
    source             TEXT NOT NULL,
    newest_at          TEXT,
    oldest_at          TEXT,
    incremental_cursor TEXT,
    incremental_since  TEXT,
    backfill_cursor    TEXT,
    backfill_complete  INTEGER NOT NULL DEFAULT 0,
    last_success_at    TEXT,
    last_error         TEXT,
    PRIMARY KEY (query, source)
);

CREATE TABLE IF NOT EXISTS projects (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL COLLATE NOCASE UNIQUE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_queries (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    query      TEXT NOT NULL REFERENCES tracked_queries(query) ON DELETE CASCADE,
    added_at   TEXT NOT NULL,
    PRIMARY KEY (project_id, query)
);
CREATE INDEX IF NOT EXISTS idx_project_queries_query ON project_queries(query);

CREATE TABLE IF NOT EXISTS alert_outbox (
    query        TEXT NOT NULL,
    mention_id   TEXT NOT NULL,
    target_key   TEXT NOT NULL,
    enqueued_at  TEXT NOT NULL,
    delivered_at TEXT,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    PRIMARY KEY (query, mention_id, target_key)
);
CREATE INDEX IF NOT EXISTS idx_alert_pending
    ON alert_outbox(target_key, query, delivered_at, enqueued_at);

CREATE TABLE IF NOT EXISTS threshold_alerts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    query        TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    target_key   TEXT NOT NULL,
    text         TEXT NOT NULL,
    payload      TEXT NOT NULL,
    triggered_at TEXT NOT NULL,
    delivered_at TEXT,
    cleared_at   TEXT,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_threshold_alert_active
    ON threshold_alerts(query, event_type, target_key) WHERE cleared_at IS NULL;

CREATE TABLE IF NOT EXISTS accounts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    platform    TEXT NOT NULL,
    handle      TEXT NOT NULL,
    credentials TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (platform, handle)
);

CREATE TABLE IF NOT EXISTS drafts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    platform     TEXT NOT NULL,
    account_id   INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('post', 'reply')),
    community    TEXT,
    title        TEXT,
    body         TEXT NOT NULL,
    reply_to     TEXT,
    query        TEXT,
    mention_id   TEXT,
    status       TEXT NOT NULL,
    remote_id    TEXT,
    remote_url   TEXT,
    error        TEXT,
    metrics      TEXT,
    metrics_at   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    approved_at  TEXT,
    published_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_drafts_status ON drafts(status, created_at);

CREATE TABLE IF NOT EXISTS activity (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    at       TEXT NOT NULL,
    action   TEXT NOT NULL,
    draft_id INTEGER,
    detail   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_activity_at ON activity(at);
`

// Store wraps one SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn := "file::memory:"
	if path != ":memory:" {
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)"
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=busy_timeout(30000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite serializes writers anyway, and a single
	// connection keeps :memory: databases coherent.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		return nil
	}
	now := stamp(time.Now())
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT OR IGNORE INTO projects (id, name, created_at, updated_at) VALUES (?, 'Default', ?, ?)`,
		DefaultProjectID, now, now,
	); err != nil {
		return err
	}
	_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database path.
func (s *Store) Path() string { return s.path }

// Check fails if SQLite cannot run a read and a small write transaction.
func (s *Store) Check(ctx context.Context) error {
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS _health (x INTEGER)"); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Rollback()
}

// Scope selects mentions by keyword, by project, or (both empty) everything.
type Scope struct {
	Query     string
	ProjectID int64
}

func (sc Scope) where(prefix string) (string, []any, error) {
	if sc.Query != "" && sc.ProjectID != 0 {
		return "", nil, errors.New("query and project are mutually exclusive")
	}
	if sc.Query != "" {
		return " " + prefix + " query = ?", []any{sc.Query}, nil
	}
	if sc.ProjectID != 0 {
		return " " + prefix + " query IN (SELECT query FROM project_queries WHERE project_id = ?)", []any{sc.ProjectID}, nil
	}
	return "", nil, nil
}

// Upsert inserts or updates mentions and returns how many were new.
//
// updateTheme=false leaves an existing row's theme untouched: used for the
// pre-cluster ingest so re-ingesting cannot transiently clear a label.
func (s *Store) Upsert(mentions []*model.Mention, updateTheme bool) (int, error) {
	now := stamp(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	observed := map[string][]string{}
	var order []string
	for _, m := range mentions {
		if _, ok := observed[m.Query]; !ok {
			order = append(order, m.Query)
		}
		if !containsStr(observed[m.Query], m.Source) {
			observed[m.Query] = append(observed[m.Query], m.Source)
		}
	}
	for _, q := range order {
		srcs, _ := json.Marshal(observed[q])
		res, err := tx.Exec(`INSERT OR IGNORE INTO tracked_queries (query, sources, created_at, updated_at, last_scanned_at)
			VALUES (?, ?, ?, ?, ?)`, q, string(srcs), now, now, now)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO project_queries (project_id, query, added_at) VALUES (?, ?, ?)`,
				DefaultProjectID, q, now); err != nil {
				return 0, err
			}
		}
	}

	themeUpdate := ""
	if updateTheme {
		themeUpdate = "theme=excluded.theme, "
	}
	upsert := `INSERT INTO mentions (id, source, query, author, title, text, url, created_at,
			score, sentiment, sentiment_score, theme, fetched_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id, query) DO UPDATE SET
			source=excluded.source, author=excluded.author, title=excluded.title,
			text=excluded.text, url=excluded.url, created_at=excluded.created_at,
			sentiment=excluded.sentiment, sentiment_score=excluded.sentiment_score,
			` + themeUpdate + `score=excluded.score, fetched_at=excluded.fetched_at`
	newCount := 0
	for _, m := range mentions {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM mentions WHERE id = ? AND query = ?`, m.ID, m.Query).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			newCount++
		} else if err != nil {
			return 0, err
		}
		var sentiment any
		if m.Sentiment != "" {
			sentiment = string(m.Sentiment)
		}
		if _, err := tx.Exec(upsert, m.ID, m.Source, m.Query, m.Author, m.Title, m.Text, m.URL,
			stamp(m.CreatedAt), m.Score, sentiment, m.SentimentScore, m.Theme, now); err != nil {
			return 0, err
		}
	}
	return newCount, tx.Commit()
}

// SaveTracking persists a keyword and its sources even when a scan finds nothing.
// A keyword with no project joins the given one, or Default.
func (s *Store) SaveTracking(query string, sources []string, projectID int64) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return errors.New("query must not be empty")
	}
	var normalized []string
	for _, src := range sources {
		src = strings.ToLower(strings.TrimSpace(src))
		if src != "" && !containsStr(normalized, src) {
			normalized = append(normalized, src)
		}
	}
	if len(normalized) == 0 {
		return errors.New("at least one source must be configured")
	}
	now := stamp(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if projectID != 0 {
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("unknown project: %d", projectID)
			}
			return err
		}
	}
	srcs, _ := json.Marshal(normalized)
	if _, err := tx.Exec(`INSERT INTO tracked_queries (query, sources, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(query) DO UPDATE SET sources = excluded.sources, updated_at = excluded.updated_at`,
		query, string(srcs), now, now); err != nil {
		return err
	}
	target := projectID
	if target == 0 {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM project_queries WHERE query = ? LIMIT 1`, query).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			target = DefaultProjectID
		} else if err != nil {
			return err
		}
	}
	if target != 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO project_queries (project_id, query, added_at) VALUES (?, ?, ?)`,
			target, query, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExistingIDs returns which of ids are already stored for query.
func (s *Store) ExistingIDs(query string, ids []string) (map[string]bool, error) {
	found := map[string]bool{}
	for start := 0; start < len(ids); start += 900 {
		chunk := ids[start:min(len(ids), start+900)]
		args := []any{query}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.Query(`SELECT id FROM mentions WHERE query = ? AND id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			found[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// MentionFilter narrows Mentions. Limit 0 means no limit.
type MentionFilter struct {
	Scope
	Source    string
	Sentiment model.Sentiment
	Limit     int
}

// Mentions returns matching mentions, newest first.
func (s *Store) Mentions(f MentionFilter) ([]*model.Mention, error) {
	where, args, err := f.Scope.where("AND")
	if err != nil {
		return nil, err
	}
	q := `SELECT id, source, query, author, title, text, url, created_at, score, sentiment, sentiment_score, theme
		FROM mentions WHERE 1=1` + where
	if f.Source != "" {
		q += " AND source = ?"
		args = append(args, f.Source)
	}
	if f.Sentiment != "" {
		q += " AND sentiment = ?"
		args = append(args, string(f.Sentiment))
	}
	q += " ORDER BY created_at DESC, id"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Mention
	for rows.Next() {
		var (
			m                   model.Mention
			author, title, text sql.NullString
			url, sent, theme    sql.NullString
			created             string
			score               sql.NullInt64
			sentScore           sql.NullFloat64
		)
		if err := rows.Scan(&m.ID, &m.Source, &m.Query, &author, &title, &text, &url, &created,
			&score, &sent, &sentScore, &theme); err != nil {
			return nil, err
		}
		m.Author, m.Title, m.URL, m.Theme = nullStr(author), nullStr(title), nullStr(url), nullStr(theme)
		m.Text = text.String
		m.CreatedAt = parseStamp(created)
		if score.Valid {
			m.Score = model.Int(score.Int64)
		}
		if sent.Valid {
			m.Sentiment = model.Sentiment(sent.String)
		}
		if sentScore.Valid {
			m.SentimentScore = model.Float(sentScore.Float64)
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// Queries lists tracked keywords (optionally within a project), most recently active first.
func (s *Store) Queries(projectID int64) ([]string, error) {
	q := `SELECT t.query FROM tracked_queries AS t LEFT JOIN mentions AS m ON m.query = t.query`
	var args []any
	if projectID != 0 {
		q += ` JOIN project_queries AS pq ON pq.query = t.query AND pq.project_id = ?`
		args = append(args, projectID)
	}
	q += ` GROUP BY t.query ORDER BY COALESCE(MAX(m.created_at), t.updated_at) DESC, t.query COLLATE NOCASE`
	return s.strings(q, args...)
}

// Tracking is a tracked keyword's saved configuration.
type Tracking struct {
	Query         string        `json:"query"`
	Sources       []string      `json:"sources"`
	CreatedAt     string        `json:"created_at"`
	UpdatedAt     string        `json:"updated_at"`
	LastScannedAt *string       `json:"last_scanned_at"`
	SourceStates  []SourceState `json:"source_states"`
}

// Tracking returns the keyword's configuration, or nil when it is unknown.
func (s *Store) Tracking(query string) (*Tracking, error) {
	var (
		t       Tracking
		srcs    string
		scanned sql.NullString
	)
	err := s.db.QueryRow(`SELECT query, sources, created_at, updated_at, last_scanned_at FROM tracked_queries WHERE query = ?`, query).
		Scan(&t.Query, &srcs, &t.CreatedAt, &t.UpdatedAt, &scanned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(srcs), &t.Sources) != nil || t.Sources == nil {
		t.Sources = []string{}
	}
	t.LastScannedAt = nullStr(scanned)
	if t.SourceStates, err = s.SourceStates(query); err != nil {
		return nil, err
	}
	return &t, nil
}

// Summary aggregates a scope.
type Summary struct {
	Total       int            `json:"total"`
	BySentiment map[string]int `json:"by_sentiment"`
	BySource    map[string]int `json:"by_source"`
	ByDay       map[string]int `json:"by_day"`
}

// Summary returns totals by sentiment, source and day.
func (s *Store) Summary(sc Scope) (Summary, error) {
	where, args, err := sc.where("WHERE")
	if err != nil {
		return Summary{}, err
	}
	out := Summary{BySentiment: map[string]int{}, BySource: map[string]int{}, ByDay: map[string]int{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mentions`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	for _, g := range []struct {
		sql string
		dst map[string]int
	}{
		{`SELECT COALESCE(sentiment, 'neutral') AS k, COUNT(*) FROM mentions` + where + ` GROUP BY k`, out.BySentiment},
		{`SELECT source, COUNT(*) FROM mentions` + where + ` GROUP BY source`, out.BySource},
		{`SELECT substr(created_at, 1, 10) AS d, COUNT(*) FROM mentions` + where + ` GROUP BY d`, out.ByDay},
	} {
		if err := s.counts(g.dst, g.sql, args...); err != nil {
			return out, err
		}
	}
	return out, nil
}

// DayPoint is one day's sentiment breakdown.
type DayPoint struct {
	Date     string `json:"date"`
	Positive int    `json:"positive"`
	Neutral  int    `json:"neutral"`
	Negative int    `json:"negative"`
	Total    int    `json:"total"`
}

// Timeseries returns per-day sentiment counts, oldest first (active days only).
func (s *Store) Timeseries(sc Scope) ([]DayPoint, error) {
	where, args, err := sc.where("WHERE")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT substr(created_at, 1, 10) AS d,
			COALESCE(SUM(sentiment = 'positive'), 0),
			COALESCE(SUM(sentiment = 'neutral' OR sentiment IS NULL), 0),
			COALESCE(SUM(sentiment = 'negative'), 0),
			COUNT(*)
		FROM mentions`+where+` GROUP BY d ORDER BY d`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DayPoint{}
	for rows.Next() {
		var p DayPoint
		if err := rows.Scan(&p.Date, &p.Positive, &p.Neutral, &p.Negative, &p.Total); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ThemeCount is a stored theme label and how many mentions carry it.
type ThemeCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Themes counts stored theme labels over the complete scope.
func (s *Store) Themes(sc Scope, limit int) ([]ThemeCount, error) {
	where, args, err := sc.where("AND")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT theme, COUNT(*) AS n FROM mentions WHERE theme IS NOT NULL AND theme != ''`+
		where+` GROUP BY theme ORDER BY n DESC, theme LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThemeCount{}
	for rows.Next() {
		var t ThemeCount
		if err := rows.Scan(&t.Label, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// NetSentiment is (positive − negative) / total, rounded to 3 places.
func NetSentiment(sum Summary) float64 {
	total := sum.Total
	if total == 0 {
		total = 1
	}
	v := float64(sum.BySentiment["positive"]-sum.BySentiment["negative"]) / float64(total)
	return roundTo(v, 3)
}

// --- helpers ----------------------------------------------------------------

func (s *Store) strings(q string, args ...any) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) counts(dst map[string]int, q string, args ...any) error {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		dst[k] = n
	}
	return rows.Err()
}

func stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

// Stamp formats t the way the store does.
func Stamp(t time.Time) string { return stamp(t) }

func parseStamp(s string) time.Time {
	for _, layout := range []string{timeLayout, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func nullStr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}
	if v < 0 {
		return -float64(int64(-v*p+0.5)) / p
	}
	return float64(int64(v*p+0.5)) / p
}

// ParseStamp parses a stored timestamp (zero time when invalid).
func ParseStamp(s string) time.Time { return parseStamp(s) }
