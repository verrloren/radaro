package store

import (
	"fmt"
	"time"
)

// SetDraftAccount pins a draft to an account, or with nil lets the outbox
// pick one at publish time. Like EditDraft it refuses drafts that are being
// or have been published; callers send the draft back to review with EditDraft.
//
// The draft and the account must be the user's, and the account must be on
// the draft's platform.
func (s *Store) SetDraftAccount(userID, id int64, accountID *int64) (*Draft, error) {
	var account any
	if accountID != nil {
		d, err := s.Draft(userID, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, fmt.Errorf("%w: draft %d", ErrNotFound, id)
		}
		a, err := s.Account(userID, *accountID)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, fmt.Errorf("%w: account %d", ErrNotFound, *accountID)
		}
		if a.Platform != d.Platform {
			return nil, fmt.Errorf("account %d is not a %s account", *accountID, d.Platform)
		}
		account = *accountID
	}
	where, wargs := owner("user_id", userID)
	res, err := s.db.Exec(`UPDATE drafts SET account_id = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?) AND `+where,
		append([]any{account, stamp(time.Now()), id, DraftPublishing, DraftPublished}, wargs...)...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		d, err := s.Draft(userID, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, fmt.Errorf("%w: draft %d", ErrNotFound, id)
		}
		return nil, fmt.Errorf("%w: draft %d is already %s", ErrConflict, id, d.Status)
	}
	return s.Draft(userID, id)
}

// PublishedDrafts lists the user's drafts on a platform published since t,
// newest first.
func (s *Store) PublishedDrafts(userID int64, platform string, since time.Time) ([]*Draft, error) {
	where, wargs := owner("user_id", userID)
	rows, err := s.rdb.Query(`SELECT `+draftColumns+` FROM drafts
		WHERE platform = ? AND status = ? AND published_at >= ? AND `+where+` ORDER BY published_at DESC`,
		append([]any{platform, DraftPublished, stamp(since)}, wargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Draft{}
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
