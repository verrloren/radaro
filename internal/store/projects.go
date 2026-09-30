package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Project is a named group of tracked keywords.
type Project struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	QueryCount   int      `json:"query_count"`
	MentionCount int      `json:"mention_count"`
	Queries      []string `json:"queries,omitempty"`
}

// ErrNotFound marks a missing project or keyword.
var ErrNotFound = errors.New("not found")

const projectSelect = `SELECT p.id, p.name, p.created_at, p.updated_at,
		COUNT(DISTINCT pq.query), COUNT(m.id)
	FROM projects AS p
	LEFT JOIN project_queries AS pq ON pq.project_id = p.id
	LEFT JOIN mentions AS m ON m.query = pq.query`

// Projects lists every project, Default first, then by name.
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.rdb.Query(projectSelect+` GROUP BY p.id
		ORDER BY CASE WHEN p.id = ? THEN 0 ELSE 1 END, p.name COLLATE NOCASE`, DefaultProjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt, &p.QueryCount, &p.MentionCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Project returns one project with its keywords, or nil when it does not exist.
func (s *Store) Project(id int64) (*Project, error) {
	var p Project
	err := s.rdb.QueryRow(projectSelect+` WHERE p.id = ? GROUP BY p.id`, id).
		Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt, &p.QueryCount, &p.MentionCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Queries, err = s.Queries(id); err != nil {
		return nil, err
	}
	return &p, nil
}

// ErrConflict marks a duplicate or protected-resource violation.
var ErrConflict = errors.New("conflict")

// CreateProject creates an empty project.
func (s *Store) CreateProject(name string) (*Project, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, errors.New("project name must not be empty")
	}
	if len([]rune(name)) > 100 {
		return nil, errors.New("project name must be at most 100 characters")
	}
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&exists)
	if err == nil {
		return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := stamp(time.Now())
	res, err := s.db.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES (?, ?, ?)`, name, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Project(id)
}

// AddQueryToProject groups an existing tracked keyword; false when already a member.
func (s *Store) AddQueryToProject(projectID int64, query string) (bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return false, errors.New("query must not be empty")
	}
	var one int
	if err := s.db.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("%w: unknown project %d", ErrNotFound, projectID)
		}
		return false, err
	}
	if err := s.db.QueryRow(`SELECT 1 FROM tracked_queries WHERE query = ?`, query).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("%w: unknown tracked keyword %q", ErrNotFound, query)
		}
		return false, err
	}
	now := stamp(time.Now())
	res, err := s.db.Exec(`INSERT OR IGNORE INTO project_queries (project_id, query, added_at) VALUES (?, ?, ?)`, projectID, query, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, err = s.db.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, now, projectID)
	}
	return n > 0, err
}

// RemoveQueryFromProject removes only the grouping; keyword and mentions stay.
func (s *Store) RemoveQueryFromProject(projectID int64, query string) (bool, error) {
	if projectID == DefaultProjectID {
		return false, fmt.Errorf("%w: keywords cannot be removed from the Default project", ErrConflict)
	}
	res, err := s.db.Exec(`DELETE FROM project_queries WHERE project_id = ? AND query = ?`, projectID, strings.TrimSpace(query))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_, err = s.db.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, stamp(time.Now()), projectID)
	}
	return n > 0, err
}

// DeleteProject deletes a grouping without deleting its keywords or mentions.
func (s *Store) DeleteProject(projectID int64) (bool, error) {
	if projectID == DefaultProjectID {
		return false, fmt.Errorf("%w: the Default project cannot be deleted", ErrConflict)
	}
	res, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
