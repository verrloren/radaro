package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Account is a connected publishing account. Credentials are stored as JSON
// in the local database, next to the rest of the user's data.
type Account struct {
	ID          int64           `json:"id"`
	Platform    string          `json:"platform"`
	Handle      string          `json:"handle"`
	Credentials json.RawMessage `json:"-"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// SaveAccount inserts or refreshes the user's account for (platform, handle).
func (s *Store) SaveAccount(userID int64, platform, handle string, credentials any) (*Account, error) {
	creds, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	now := stamp(time.Now())
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Not ON CONFLICT: the unique key holds a NULL owner for user 0, and
	// NULLs never conflict.
	var id int64
	err = tx.QueryRow(`SELECT id FROM accounts WHERE user_id IS ? AND platform = ? AND handle = ?`,
		ownerValue(userID), platform, handle).Scan(&id)
	switch {
	case err == nil:
		_, err = tx.Exec(`UPDATE accounts SET credentials = ?, updated_at = ? WHERE id = ?`, string(creds), now, id)
	case errors.Is(err, sql.ErrNoRows):
		var res sql.Result
		res, err = tx.Exec(`INSERT INTO accounts (user_id, platform, handle, credentials, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			ownerValue(userID), platform, handle, string(creds), now, now)
		if err == nil {
			id, _ = res.LastInsertId()
		}
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.restrict()
	return s.Account(userID, id)
}

// UpdateAccountCredentials replaces an account's stored credentials (e.g. a rotated refresh token).
func (s *Store) UpdateAccountCredentials(id int64, credentials any) error {
	creds, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE accounts SET credentials = ?, updated_at = ? WHERE id = ?`, string(creds), stamp(time.Now()), id)
	return err
}

const accountColumns = `id, platform, handle, credentials, created_at, updated_at`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	var creds string
	if err := row.Scan(&a.ID, &a.Platform, &a.Handle, &creds, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Credentials = json.RawMessage(creds)
	return &a, nil
}

// Account returns one of the user's accounts, or nil.
func (s *Store) Account(userID, id int64) (*Account, error) {
	where, args := owner("user_id", userID)
	a, err := scanAccount(s.rdb.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ? AND `+where, append([]any{id}, args...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// Accounts lists the user's connected accounts, optionally for one platform.
func (s *Store) Accounts(userID int64, platform string) ([]*Account, error) {
	where, args := owner("user_id", userID)
	q := `SELECT ` + accountColumns + ` FROM accounts WHERE ` + where
	if platform != "" {
		q += ` AND platform = ?`
		args = append(args, platform)
	}
	rows, err := s.rdb.Query(q+` ORDER BY platform, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAccount removes one of the user's accounts; its drafts keep their text.
func (s *Store) DeleteAccount(userID, id int64) (bool, error) {
	where, args := owner("user_id", userID)
	res, err := s.db.Exec(`DELETE FROM accounts WHERE id = ? AND `+where, append([]any{id}, args...)...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Draft statuses. Only approved drafts may be published.
const (
	DraftPending    = "draft"
	DraftApproved   = "approved"
	DraftPublishing = "publishing"
	DraftPublished  = "published"
	DraftFailed     = "failed"
	DraftSkipped    = "skipped"
)

// Draft is a post or reply written for one platform.
type Draft struct {
	ID          int64          `json:"id"`
	Platform    string         `json:"platform"`
	AccountID   *int64         `json:"account_id"`
	Kind        string         `json:"kind"` // post | reply
	Community   *string        `json:"community"`
	Title       *string        `json:"title"`
	Body        string         `json:"body"`
	ReplyTo     *string        `json:"reply_to"`
	Query       *string        `json:"query"`
	MentionID   *string        `json:"mention_id"`
	Status      string         `json:"status"`
	RemoteID    *string        `json:"remote_id"`
	RemoteURL   *string        `json:"remote_url"`
	Error       *string        `json:"error"`
	Metrics     map[string]any `json:"metrics"`
	MetricsAt   *string        `json:"metrics_at"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	ApprovedAt  *string        `json:"approved_at"`
	PublishedAt *string        `json:"published_at"`
}

const draftColumns = `id, platform, account_id, kind, community, title, body, reply_to, query, mention_id, status,
	remote_id, remote_url, error, metrics, metrics_at, created_at, updated_at, approved_at, published_at`

func scanDraft(row interface{ Scan(...any) error }) (*Draft, error) {
	var (
		d                                    Draft
		account                              sql.NullInt64
		community, title, replyTo, query     sql.NullString
		mention, remoteID, remoteURL, errMsg sql.NullString
		metrics, metricsAt, approved, pub    sql.NullString
	)
	if err := row.Scan(&d.ID, &d.Platform, &account, &d.Kind, &community, &title, &d.Body, &replyTo, &query, &mention,
		&d.Status, &remoteID, &remoteURL, &errMsg, &metrics, &metricsAt, &d.CreatedAt, &d.UpdatedAt, &approved, &pub); err != nil {
		return nil, err
	}
	if account.Valid {
		d.AccountID = &account.Int64
	}
	d.Community, d.Title, d.ReplyTo, d.Query = nullStr(community), nullStr(title), nullStr(replyTo), nullStr(query)
	d.MentionID, d.RemoteID, d.RemoteURL, d.Error = nullStr(mention), nullStr(remoteID), nullStr(remoteURL), nullStr(errMsg)
	d.MetricsAt, d.ApprovedAt, d.PublishedAt = nullStr(metricsAt), nullStr(approved), nullStr(pub)
	if metrics.Valid {
		_ = json.Unmarshal([]byte(metrics.String), &d.Metrics)
	}
	return &d, nil
}

// NewDraft is the input for CreateDraft.
type NewDraft struct {
	UserID    int64
	Platform  string
	AccountID int64
	Kind      string
	Community string
	Title     string
	Body      string
	ReplyTo   string
	Query     string
	MentionID string
}

// CreateDraft stores a new draft in the "draft" status.
func (s *Store) CreateDraft(n NewDraft) (*Draft, error) {
	if strings.TrimSpace(n.Body) == "" {
		return nil, errors.New("draft body must not be empty")
	}
	if n.Kind != "post" && n.Kind != "reply" {
		return nil, errors.New("draft kind must be post or reply")
	}
	if n.Kind == "reply" && strings.TrimSpace(n.ReplyTo) == "" {
		return nil, errors.New("a reply needs --reply-to <url>")
	}
	var account any
	if n.AccountID != 0 {
		a, err := s.Account(n.UserID, n.AccountID)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, fmt.Errorf("%w: account %d", ErrNotFound, n.AccountID)
		}
		account = n.AccountID
	}
	now := stamp(time.Now())
	res, err := s.db.Exec(`INSERT INTO drafts (user_id, platform, account_id, kind, community, title, body, reply_to, query, mention_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerValue(n.UserID), n.Platform, account, n.Kind, nullIfEmpty(n.Community), nullIfEmpty(n.Title), n.Body, nullIfEmpty(n.ReplyTo),
		nullIfEmpty(n.Query), nullIfEmpty(n.MentionID), DraftPending, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Draft(n.UserID, id)
}

// Draft returns one of the user's drafts, or nil.
func (s *Store) Draft(userID, id int64) (*Draft, error) {
	where, args := owner("user_id", userID)
	d, err := scanDraft(s.rdb.QueryRow(`SELECT `+draftColumns+` FROM drafts WHERE id = ? AND `+where, append([]any{id}, args...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// Drafts lists the user's drafts newest first, optionally filtered by status.
func (s *Store) Drafts(userID int64, status string, limit int) ([]*Draft, error) {
	where, args := owner("user_id", userID)
	q := `SELECT ` + draftColumns + ` FROM drafts WHERE ` + where
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.rdb.Query(q, args...)
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

// DraftEdit changes a draft's text. Nil fields are left alone.
type DraftEdit struct {
	Title     *string
	Body      *string
	Community *string
}

// EditDraft updates text and sends an approved or failed draft back to review.
func (s *Store) EditDraft(userID, id int64, e DraftEdit) (*Draft, error) {
	d, err := s.Draft(userID, id)
	if err != nil || d == nil {
		return d, err
	}
	if d.Status == DraftPublished || d.Status == DraftPublishing {
		return nil, fmt.Errorf("%w: draft %d is already %s", ErrConflict, id, d.Status)
	}
	if e.Body != nil && strings.TrimSpace(*e.Body) == "" {
		return nil, errors.New("draft body must not be empty")
	}
	sets := []string{"status = ?", "approved_at = NULL", "error = NULL", "updated_at = ?"}
	args := []any{DraftPending, stamp(time.Now())}
	if e.Title != nil {
		sets, args = append(sets, "title = ?"), append(args, nullIfEmpty(*e.Title))
	}
	if e.Body != nil {
		sets, args = append(sets, "body = ?"), append(args, *e.Body)
	}
	if e.Community != nil {
		sets, args = append(sets, "community = ?"), append(args, nullIfEmpty(*e.Community))
	}
	if _, err := s.db.Exec(`UPDATE drafts SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
		return nil, err
	}
	return s.Draft(userID, id)
}

// transition moves one of the user's drafts from an allowed status to next.
func (s *Store) transition(userID, id int64, next string, from []string, extra string, args ...any) (*Draft, error) {
	d, err := s.Draft(userID, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("%w: draft %d", ErrNotFound, id)
	}
	if !containsStr(from, d.Status) {
		return nil, fmt.Errorf("%w: draft %d is %s", ErrConflict, id, d.Status)
	}
	now := stamp(time.Now())
	q := `UPDATE drafts SET status = ?, updated_at = ?` + extra + ` WHERE id = ? AND status = ?`
	res, err := s.db.Exec(q, append(append([]any{next, now}, args...), id, d.Status)...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: draft %d changed concurrently", ErrConflict, id)
	}
	return s.Draft(userID, id)
}

// ApproveDraft marks a draft (or a failed one being retried) as approved for publishing.
func (s *Store) ApproveDraft(userID, id int64) (*Draft, error) {
	return s.transition(userID, id, DraftApproved, []string{DraftPending, DraftFailed}, `, approved_at = ?, error = NULL`, stamp(time.Now()))
}

// SkipDraft discards a draft that has not been published.
func (s *Store) SkipDraft(userID, id int64) (*Draft, error) {
	return s.transition(userID, id, DraftSkipped, []string{DraftPending, DraftApproved, DraftFailed}, ``)
}

// BeginPublish claims an approved draft before any network call, so a crash
// mid-publish leaves it visibly "publishing" instead of silently re-posting.
func (s *Store) BeginPublish(userID, id int64) (*Draft, error) {
	return s.transition(userID, id, DraftPublishing, []string{DraftApproved}, ``)
}

// FinishPublish records the outcome of a publish claimed with BeginPublish.
func (s *Store) FinishPublish(id int64, remoteID, remoteURL string, publishErr error) (*Draft, error) {
	if publishErr != nil {
		return s.transition(0, id, DraftFailed, []string{DraftPublishing}, `, error = ?`, truncate(publishErr.Error(), 1000))
	}
	return s.transition(0, id, DraftPublished, []string{DraftPublishing}, `, remote_id = ?, remote_url = ?, published_at = ?, error = NULL`,
		remoteID, nullIfEmpty(remoteURL), stamp(time.Now()))
}

// SaveMetrics stores the latest metrics snapshot for a published draft.
func (s *Store) SaveMetrics(id int64, metrics map[string]any) error {
	body, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE drafts SET metrics = ?, metrics_at = ? WHERE id = ?`, string(body), stamp(time.Now()), id)
	return err
}

// Activity is one entry of the action log.
type Activity struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Action  string `json:"action"`
	DraftID *int64 `json:"draft_id"`
	Detail  string `json:"detail"`
}

// LogActivity appends to the user's action log. draftID 0 means none.
func (s *Store) LogActivity(userID int64, action string, draftID int64, detail string) error {
	var d any
	if draftID != 0 {
		d = draftID
	}
	_, err := s.db.Exec(`INSERT INTO activity (user_id, at, action, draft_id, detail) VALUES (?, ?, ?, ?, ?)`,
		ownerValue(userID), stamp(time.Now()), action, d, detail)
	return err
}

// Activities returns the user's newest log entries first.
func (s *Store) Activities(userID int64, limit int) ([]Activity, error) {
	where, args := owner("user_id", userID)
	rows, err := s.rdb.Query(`SELECT id, at, action, draft_id, detail FROM activity WHERE `+where+` ORDER BY id DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var draft sql.NullInt64
		if err := rows.Scan(&a.ID, &a.At, &a.Action, &draft, &a.Detail); err != nil {
			return nil, err
		}
		if draft.Valid {
			a.DraftID = &draft.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DraftedMentionIDs returns mention IDs the user already has a non-skipped draft for.
func (s *Store) DraftedMentionIDs(userID int64) (map[string]bool, error) {
	where, args := owner("user_id", userID)
	ids, err := s.strings(`SELECT DISTINCT mention_id FROM drafts WHERE mention_id IS NOT NULL AND status != ? AND `+where,
		append([]any{DraftSkipped}, args...)...)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// DraftCounts returns how many of the user's drafts are in each status.
func (s *Store) DraftCounts(userID int64) (map[string]int, error) {
	where, args := owner("user_id", userID)
	out := map[string]int{}
	return out, s.counts(out, `SELECT status, COUNT(*) FROM drafts WHERE `+where+` GROUP BY status`, args...)
}
