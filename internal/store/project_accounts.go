package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Binding is an account in a project's pool for a platform. A project may
// pool several accounts per platform; publishing picks among them.
type Binding struct {
	Platform string   `json:"platform"`
	Account  *Account `json:"account"`
	BoundAt  string   `json:"bound_at"`
}

// ProjectBindings lists the accounts pooled in one of the user's projects,
// by platform, oldest binding first.
func (s *Store) ProjectBindings(userID, projectID int64) ([]Binding, error) {
	if _, err := ownedProject(s.rdb, userID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.rdb.Query(`SELECT `+accountColumnsAs+`, b.platform, b.bound_at
		FROM project_accounts AS b JOIN accounts AS a ON a.id = b.account_id
		WHERE b.project_id = ? ORDER BY b.platform, b.bound_at, a.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		a, err := scanAccount(rows, &b.Platform, &b.BoundAt)
		if err != nil {
			return nil, err
		}
		b.Account = a
		out = append(out, b)
	}
	return out, rows.Err()
}

// ProjectPool is the project's pool for one platform: the accounts drafts of
// that project publish from. Empty when none is bound.
func (s *Store) ProjectPool(userID, projectID int64, platform string) ([]*Account, error) {
	bs, err := s.ProjectBindings(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := []*Account{}
	for _, b := range bs {
		if b.Platform == platform {
			out = append(out, b.Account)
		}
	}
	return out, nil
}

// BindAccount adds the account to the project's pool for its platform;
// binding it again changes nothing. Both must belong to the same user.
func (s *Store) BindAccount(userID, projectID, accountID int64) (*Account, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := ownedProject(tx, userID, projectID); err != nil {
		return nil, err
	}
	var platform string
	err = tx.QueryRow(`SELECT a.platform FROM accounts AS a JOIN projects AS p ON p.id = ?
		WHERE a.id = ? AND a.user_id IS p.user_id`, projectID, accountID).Scan(&platform)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: account %d", ErrNotFound, accountID)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO project_accounts (project_id, platform, account_id, bound_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, account_id) DO NOTHING`,
		projectID, platform, accountID, stamp(time.Now())); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Account(userID, accountID)
}

// UnbindAccount empties the project's pool for a platform.
func (s *Store) UnbindAccount(userID, projectID int64, platform string) (bool, error) {
	if _, err := ownedProject(s.db, userID, projectID); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`DELETE FROM project_accounts WHERE project_id = ? AND platform = ?`, projectID, platform)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnbindProjectAccount takes one account out of the project's pool.
func (s *Store) UnbindProjectAccount(userID, projectID, accountID int64) (bool, error) {
	if _, err := ownedProject(s.db, userID, projectID); err != nil {
		return false, err
	}
	res, err := s.db.Exec(`DELETE FROM project_accounts WHERE project_id = ? AND account_id = ?`, projectID, accountID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// poolAccount is the only account in a project's pool for a platform, 0 when
// the pool is empty, and ErrConflict when it holds several.
func poolAccount(q *sql.DB, projectID int64, platform string) (int64, error) {
	rows, err := q.Query(`SELECT account_id FROM project_accounts WHERE project_id = ? AND platform = ?`, projectID, platform)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	switch len(ids) {
	case 0:
		return 0, nil
	case 1:
		return ids[0], nil
	}
	return 0, fmt.Errorf("%w: the project pools several %s accounts; pick one for the draft", ErrConflict, platform)
}

// PublishAccount is the account a draft goes out with when no limits are
// weighed: the one it names, else the only one in its project's pool, else the
// user's only account on that platform. The outbox picks among several.
func (s *Store) PublishAccount(userID int64, d *Draft) (*Account, error) {
	id := int64(0)
	if d.AccountID != nil {
		id = *d.AccountID
	} else if d.ProjectID != nil {
		var err error
		if id, err = poolAccount(s.rdb, *d.ProjectID, d.Platform); err != nil {
			return nil, err
		}
	}
	if id != 0 {
		acc, err := s.Account(userID, id)
		if err != nil {
			return nil, err
		}
		if acc == nil {
			return nil, fmt.Errorf("%w: account %d no longer exists", ErrNotFound, id)
		}
		return acc, nil
	}
	accs, err := s.Accounts(userID, d.Platform)
	if err != nil {
		return nil, err
	}
	switch len(accs) {
	case 0:
		return nil, fmt.Errorf("%w: no %s account connected", ErrNotFound, d.Platform)
	case 1:
		return accs[0], nil
	}
	return nil, fmt.Errorf("%w: several %s accounts are connected; bind one to the project or pick one for the draft", ErrConflict, d.Platform)
}
