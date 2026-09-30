package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// SourceState is the durable per-keyword, per-source scan progress.
type SourceState struct {
	Source            string  `json:"source"`
	NewestAt          *string `json:"newest_at"`
	OldestAt          *string `json:"oldest_at"`
	IncrementalCursor string  `json:"-"`
	IncrementalSince  string  `json:"-"`
	BackfillCursor    string  `json:"-"`
	BackfillComplete  bool    `json:"backfill_complete"`
	LastSuccessAt     *string `json:"last_success_at"`
	LastError         *string `json:"last_error"`
}

const stateColumns = `source, newest_at, oldest_at, incremental_cursor, incremental_since,
	backfill_cursor, backfill_complete, last_success_at, last_error`

func scanState(row interface{ Scan(...any) error }) (SourceState, error) {
	var (
		st                            SourceState
		newest, oldest, incCur, since sql.NullString
		bfCur, success, lastErr       sql.NullString
		complete                      int
	)
	err := row.Scan(&st.Source, &newest, &oldest, &incCur, &since, &bfCur, &complete, &success, &lastErr)
	st.NewestAt, st.OldestAt = nullStr(newest), nullStr(oldest)
	st.IncrementalCursor, st.IncrementalSince, st.BackfillCursor = incCur.String, since.String, bfCur.String
	st.BackfillComplete = complete != 0
	st.LastSuccessAt, st.LastError = nullStr(success), nullStr(lastErr)
	return st, err
}

// SourceState returns the state for one keyword/source pair (zero value if none).
func (s *Store) SourceState(query, source string) (SourceState, error) {
	st, err := scanState(s.rdb.QueryRow(`SELECT `+stateColumns+` FROM source_scan_state WHERE query = ? AND source = ?`, query, source))
	if errors.Is(err, sql.ErrNoRows) {
		return SourceState{Source: source}, nil
	}
	return st, err
}

// SourceStates returns every source state for a keyword.
func (s *Store) SourceStates(query string) ([]SourceState, error) {
	rows, err := s.rdb.Query(`SELECT `+stateColumns+` FROM source_scan_state WHERE query = ? ORDER BY source`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceState{}
	for rows.Next() {
		st, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// RecordSourceSuccess advances a source's cursors after its rows are committed.
//
// The first recent scan establishes both temporal bounds; its next page is
// historical, so it seeds the backfill cursor rather than the incremental one.
func (s *Store) RecordSourceSuccess(query, source string, mentions []*model.Mention, backfill bool, nextCursor string, incrementalSince time.Time) error {
	st, err := s.SourceState(query, source)
	if err != nil {
		return err
	}
	newest, oldest := derefOr(st.NewestAt), derefOr(st.OldestAt)
	for _, m := range mentions {
		ts := stamp(m.CreatedAt)
		if newest == "" || ts > newest {
			newest = ts
		}
		if oldest == "" || ts < oldest {
			oldest = ts
		}
	}
	incCursor, incSince := st.IncrementalCursor, st.IncrementalSince
	bfCursor, bfComplete := st.BackfillCursor, st.BackfillComplete
	switch {
	case backfill:
		bfCursor, bfComplete = nextCursor, nextCursor == ""
	case st.NewestAt == nil:
		bfCursor, bfComplete = nextCursor, nextCursor == ""
		incCursor, incSince = "", ""
	default:
		incCursor, incSince = nextCursor, ""
		if nextCursor != "" && !incrementalSince.IsZero() {
			incSince = stamp(incrementalSince)
		}
	}
	now := stamp(time.Now())
	_, err = s.db.Exec(`INSERT INTO source_scan_state (query, source, newest_at, oldest_at, incremental_cursor,
			incremental_since, backfill_cursor, backfill_complete, last_success_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(query, source) DO UPDATE SET
			newest_at = excluded.newest_at, oldest_at = excluded.oldest_at,
			incremental_cursor = excluded.incremental_cursor, incremental_since = excluded.incremental_since,
			backfill_cursor = excluded.backfill_cursor, backfill_complete = excluded.backfill_complete,
			last_success_at = excluded.last_success_at, last_error = NULL`,
		query, source, nullIfEmpty(newest), nullIfEmpty(oldest), nullIfEmpty(incCursor), nullIfEmpty(incSince),
		nullIfEmpty(bfCursor), boolInt(bfComplete), now)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE tracked_queries SET last_scanned_at = ?, updated_at = ? WHERE query = ?`, now, now, query)
	return err
}

// RecordSourceError stores an inspectable error without moving any cursor.
func (s *Store) RecordSourceError(query, source, msg string) error {
	_, err := s.db.Exec(`INSERT INTO source_scan_state (query, source, last_error) VALUES (?, ?, ?)
		ON CONFLICT(query, source) DO UPDATE SET last_error = excluded.last_error`, query, source, truncate(msg, 500))
	return err
}

func derefOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
