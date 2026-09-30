package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Limits per user, so one account cannot make the server scan without end.
const (
	MaxProjectsPerUser    = 100
	MaxKeywordsPerProject = 500
	maxKeywordLength      = 200
)

// Keyword is one tracked keyword inside a project.
type Keyword struct {
	ID            int64    `json:"id"`
	ProjectID     int64    `json:"project_id"`
	Query         string   `json:"query"`
	Sources       []string `json:"sources"`
	AddedAt       string   `json:"added_at"`
	LastScannedAt *string  `json:"last_scanned_at"`
	MentionCount  int      `json:"mention_count"`
}

// CleanKeyword collapses whitespace and checks the length.
func CleanKeyword(raw string) (string, error) {
	q := strings.Join(strings.Fields(raw), " ")
	if q == "" {
		return "", errors.New("keyword must not be empty")
	}
	if len([]rune(q)) > maxKeywordLength {
		return "", fmt.Errorf("keyword must be at most %d characters", maxKeywordLength)
	}
	return q, nil
}

// Keywords lists a project's keywords in the order they were added.
func (s *Store) Keywords(userID, projectID int64) ([]Keyword, error) {
	if _, err := ownedProject(s.rdb, userID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.rdb.Query(`SELECT k.id, k.project_id, k.query, t.sources, k.added_at, t.last_scanned_at,
			(SELECT COUNT(*) FROM mentions AS m WHERE m.query = k.query)
		FROM project_keywords AS k JOIN tracked_queries AS t ON t.query = k.query
		WHERE k.project_id = ? ORDER BY k.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Keyword{}
	for rows.Next() {
		var (
			k       Keyword
			srcs    string
			scanned sql.NullString
		)
		if err := rows.Scan(&k.ID, &k.ProjectID, &k.Query, &srcs, &k.AddedAt, &scanned, &k.MentionCount); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(srcs), &k.Sources) != nil || k.Sources == nil {
			k.Sources = []string{}
		}
		k.LastScannedAt = nullStr(scanned)
		out = append(out, k)
	}
	return out, rows.Err()
}

// AddKeywords adds keywords to a project, registering new ones for scanning
// with the given sources. Keywords already in the project (in any letter
// case) are skipped; the result says how many were added.
func (s *Store) AddKeywords(userID, projectID int64, queries, sources []string) (int, error) {
	var cleaned []string
	for _, raw := range queries {
		q, err := CleanKeyword(raw)
		if err != nil {
			return 0, err
		}
		cleaned = append(cleaned, q)
	}
	if len(cleaned) == 0 {
		return 0, errors.New("keyword must not be empty")
	}
	srcs, _ := json.Marshal(normalizeSources(sources))
	now := stamp(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := ownedProject(tx, userID, projectID); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM project_keywords WHERE project_id = ?`, projectID).Scan(&count); err != nil {
		return 0, err
	}
	added := 0
	for _, q := range cleaned {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM project_keywords WHERE project_id = ? AND query = ? COLLATE NOCASE`, projectID, q).Scan(&one)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if count+added >= MaxKeywordsPerProject {
			return 0, fmt.Errorf("%w: a project holds at most %d keywords", ErrConflict, MaxKeywordsPerProject)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tracked_queries (query, sources, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			q, string(srcs), now, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO project_keywords (project_id, query, added_at) VALUES (?, ?, ?)`, projectID, q, now); err != nil {
			return 0, err
		}
		added++
	}
	if added > 0 {
		if _, err := tx.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, now, projectID); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

// RemoveKeyword takes a keyword out of a project. Its mentions stay, shared
// with anyone else tracking it.
func (s *Store) RemoveKeyword(userID, projectID, keywordID int64) (bool, error) {
	if _, err := ownedProject(s.db, userID, projectID); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`DELETE FROM project_keywords WHERE id = ? AND project_id = ?`, keywordID, projectID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, err = s.db.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, stamp(time.Now()), projectID)
	}
	return n > 0, err
}

func normalizeSources(sources []string) []string {
	out := []string{}
	for _, src := range sources {
		src = strings.ToLower(strings.TrimSpace(src))
		if src != "" && !containsStr(out, src) {
			out = append(out, src)
		}
	}
	return out
}
