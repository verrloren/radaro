package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Project is a named group of tracked keywords owned by one user.
type Project struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	IsDefault    bool     `json:"is_default"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	QueryCount   int      `json:"query_count"`
	MentionCount int      `json:"mention_count"`
	Queries      []string `json:"queries,omitempty"`
}

var (
	// ErrNotFound marks a missing resource, or one owned by someone else.
	ErrNotFound = errors.New("not found")
	// ErrConflict marks a duplicate or protected-resource violation.
	ErrConflict = errors.New("conflict")
)

// owner restricts col to one user's rows. User 0 is the server itself
// (admin commands, demo data): it sees every row. HTTP handlers always pass
// the signed-in user, whose id is at least 1.
func owner(col string, userID int64) (string, []any) {
	if userID == 0 {
		return "1=1", nil
	}
	return col + " = ?", []any{userID}
}

// ownerValue is what a new row stores as its owner.
func ownerValue(userID int64) any {
	if userID == 0 {
		return nil
	}
	return userID
}

const projectSelect = `SELECT p.id, p.name, p.is_default, p.created_at, p.updated_at,
		COUNT(DISTINCT pq.query), COUNT(m.id)
	FROM projects AS p
	LEFT JOIN project_keywords AS pq ON pq.project_id = p.id
	LEFT JOIN mentions AS m ON m.query = pq.query`

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt, &p.QueryCount, &p.MentionCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Projects lists a user's projects, Default first, then by name.
func (s *Store) Projects(userID int64) ([]Project, error) {
	where, args := owner("p.user_id", userID)
	rows, err := s.rdb.Query(projectSelect+` WHERE `+where+` GROUP BY p.id
		ORDER BY p.is_default DESC, p.name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Project returns one of the user's projects with its keywords, or nil.
func (s *Store) Project(userID, id int64) (*Project, error) {
	where, args := owner("p.user_id", userID)
	p, err := scanProject(s.rdb.QueryRow(projectSelect+` WHERE p.id = ? AND `+where+` GROUP BY p.id`,
		append([]any{id}, args...)...))
	if err != nil || p == nil {
		return nil, err
	}
	if p.Queries, err = s.Queries(userID, id); err != nil {
		return nil, err
	}
	return p, nil
}

// DefaultProjectFor is the user's Default project; user 0 gets the instance's.
func (s *Store) DefaultProjectFor(userID int64) (int64, error) {
	return defaultProject(s.rdb, userID)
}

// querier is a *sql.DB or *sql.Tx. Code inside a write transaction must read
// through the transaction: the writer has one connection, and in :memory:
// mode the read pool is that same connection.
type querier interface {
	QueryRow(string, ...any) *sql.Row
}

func defaultProject(q querier, userID int64) (int64, error) {
	if userID == 0 {
		return DefaultProjectID, nil
	}
	var id int64
	err := q.QueryRow(`SELECT id FROM projects WHERE user_id = ? AND is_default = 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: user %d has no Default project", ErrNotFound, userID)
	}
	return id, err
}

func cleanProjectName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", errors.New("project name must not be empty")
	}
	if len([]rune(name)) > 100 {
		return "", errors.New("project name must be at most 100 characters")
	}
	return name, nil
}

// CreateProject creates an empty project for the user.
func (s *Store) CreateProject(userID int64, name string) (*Project, error) {
	name, err := cleanProjectName(name)
	if err != nil {
		return nil, err
	}
	if userID != 0 {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE user_id = ?`, userID).Scan(&n); err != nil {
			return nil, err
		}
		if n >= MaxProjectsPerUser {
			return nil, fmt.Errorf("%w: at most %d projects per user", ErrConflict, MaxProjectsPerUser)
		}
	}
	var exists int
	err = s.db.QueryRow(`SELECT 1 FROM projects WHERE user_id IS ? AND name = ?`, ownerValue(userID), name).Scan(&exists)
	if err == nil {
		return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := stamp(time.Now())
	res, err := s.db.Exec(`INSERT INTO projects (user_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		ownerValue(userID), name, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Project(userID, id)
}

// RenameProject renames one of the user's projects.
func (s *Store) RenameProject(userID, id int64, name string) (*Project, error) {
	name, err := cleanProjectName(name)
	if err != nil {
		return nil, err
	}
	p, err := s.Project(userID, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("%w: project %d", ErrNotFound, id)
	}
	var clash int64
	err = s.db.QueryRow(`SELECT id FROM projects WHERE user_id IS (SELECT user_id FROM projects WHERE id = ?) AND name = ? AND id != ?`,
		id, name, id).Scan(&clash)
	if err == nil {
		return nil, fmt.Errorf("%w: a project named %q already exists", ErrConflict, name)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE projects SET name = ?, updated_at = ? WHERE id = ?`, name, stamp(time.Now()), id); err != nil {
		return nil, err
	}
	return s.Project(userID, id)
}

// ownedProject fails with ErrNotFound unless the project exists and belongs
// to the user, and reports whether it is their Default project.
func ownedProject(q querier, userID, projectID int64) (isDefault bool, err error) {
	where, args := owner("user_id", userID)
	err = q.QueryRow(`SELECT is_default FROM projects WHERE id = ? AND `+where, append([]any{projectID}, args...)...).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: project %d", ErrNotFound, projectID)
	}
	return isDefault, err
}

// AddQueryToProject adds one keyword; false when it is already in the project.
func (s *Store) AddQueryToProject(userID, projectID int64, query string, sources []string) (bool, error) {
	n, err := s.AddKeywords(userID, projectID, []string{query}, sources)
	return n > 0, err
}

// RemoveQueryFromProject removes a keyword by text; its mentions stay.
func (s *Store) RemoveQueryFromProject(userID, projectID int64, query string) (bool, error) {
	if _, err := ownedProject(s.db, userID, projectID); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`DELETE FROM project_keywords WHERE project_id = ? AND query = ?`, projectID, strings.TrimSpace(query))
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
func (s *Store) DeleteProject(userID, projectID int64) (bool, error) {
	isDefault, err := ownedProject(s.db, userID, projectID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if isDefault {
		return false, fmt.Errorf("%w: the Default project cannot be deleted", ErrConflict)
	}
	res, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
