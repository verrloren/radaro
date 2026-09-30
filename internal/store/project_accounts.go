package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Binding is the account a project publishes (and scans) with on a platform.
type Binding struct {
	Platform string   `json:"platform"`
	Account  *Account `json:"account"`
	BoundAt  string   `json:"bound_at"`
}

// ProjectBindings lists the accounts bound to one of the user's projects.
func (s *Store) ProjectBindings(userID, projectID int64) ([]Binding, error) {
	if _, err := ownedProject(s.rdb, userID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.rdb.Query(`SELECT b.platform, b.bound_at, a.id, a.platform, a.handle, a.credentials, a.created_at, a.updated_at
		FROM project_accounts AS b JOIN accounts AS a ON a.id = b.account_id
		WHERE b.project_id = ? ORDER BY b.platform`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		var a Account
		var creds string
		if err := rows.Scan(&b.Platform, &b.BoundAt, &a.ID, &a.Platform, &a.Handle, &creds, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Credentials = []byte(creds)
		b.Account = &a
		out = append(out, b)
	}
	return out, rows.Err()
}

// BindAccount makes account the project's account for its platform,
// replacing any other. Both must belong to the same user.
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
		ON CONFLICT(project_id, platform) DO UPDATE SET account_id = excluded.account_id, bound_at = excluded.bound_at`,
		projectID, platform, accountID, stamp(time.Now())); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Account(userID, accountID)
}

// UnbindAccount clears the project's account for a platform.
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

// boundAccount is the account bound to a project for a platform, or 0.
func boundAccount(q querier, projectID int64, platform string) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT account_id FROM project_accounts WHERE project_id = ? AND platform = ?`, projectID, platform).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// PublishAccount is the account a draft goes out with: the one it names,
// else the one bound to its project, else the user's only account on that
// platform.
func (s *Store) PublishAccount(userID int64, d *Draft) (*Account, error) {
	id := int64(0)
	if d.AccountID != nil {
		id = *d.AccountID
	} else if d.ProjectID != nil {
		var err error
		if id, err = boundAccount(s.rdb, *d.ProjectID, d.Platform); err != nil {
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
