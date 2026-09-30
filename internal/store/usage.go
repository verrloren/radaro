package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AccountUpdate changes an account's own settings. Nil fields are left
// alone; a limit of 0 resets it to the platform default.
type AccountUpdate struct {
	Paused             *bool
	DailyLimit         *int
	MinIntervalSec     *int
	CommunityCooldownH *int
	// StatusDetail replaces the status detail, e.g. why an account was paused.
	StatusDetail *string
}

// UpdateAccount applies u to one of the user's accounts and returns it, or
// nil if the user has no such account.
func (s *Store) UpdateAccount(userID, id int64, u AccountUpdate) (*Account, error) {
	sets := []string{"updated_at = ?"}
	args := []any{stamp(time.Now())}
	if u.Paused != nil {
		sets, args = append(sets, "paused = ?"), append(args, *u.Paused)
	}
	for _, f := range []struct {
		col string
		v   *int
	}{{"daily_limit", u.DailyLimit}, {"min_interval_sec", u.MinIntervalSec}, {"community_cooldown_h", u.CommunityCooldownH}} {
		if f.v == nil {
			continue
		}
		if *f.v < 0 {
			return nil, fmt.Errorf("%s must not be negative", f.col)
		}
		var v any
		if *f.v > 0 {
			v = *f.v
		}
		sets, args = append(sets, f.col+" = ?"), append(args, v)
	}
	if u.StatusDetail != nil {
		sets, args = append(sets, "status_detail = ?"), append(args, nullIfEmpty(*u.StatusDetail))
	}
	where, wargs := owner("user_id", userID)
	res, err := s.db.Exec(`UPDATE accounts SET `+strings.Join(sets, ", ")+` WHERE id = ? AND `+where, append(append(args, id), wargs...)...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.Account(userID, id)
}

// SetAccountStatus records a health result and sets checked_at. It is the
// server's own bookkeeping (health checks, publish results), so it takes no
// user: callers have already resolved the account for its owner. A zero
// limitedUntil clears it. A change of status is appended to
// account_status_events; changed reports whether it was one.
func (s *Store) SetAccountStatus(id int64, status, detail string, limitedUntil time.Time) (changed bool, err error) {
	switch status {
	case AccountUnknown, AccountLive, AccountInvalid, AccountSuspended, AccountLimited:
	default:
		return false, fmt.Errorf("unknown account status %q", status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var prev, platform string
	err = tx.QueryRow(`SELECT status, platform FROM accounts WHERE id = ?`, id).Scan(&prev, &platform)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: account %d", ErrNotFound, id)
	}
	if err != nil {
		return false, err
	}
	now := stamp(time.Now())
	var until any
	if !limitedUntil.IsZero() {
		until = stamp(limitedUntil)
	}
	if _, err := tx.Exec(`UPDATE accounts SET status = ?, status_detail = ?, checked_at = ?, limited_until = ? WHERE id = ?`,
		status, nullIfEmpty(detail), now, until, id); err != nil {
		return false, err
	}
	if prev != status {
		if _, err := tx.Exec(`INSERT INTO account_status_events (account_id, platform, at, status, detail) VALUES (?, ?, ?, ?, ?)`,
			id, platform, now, status, detail); err != nil {
			return false, err
		}
	}
	return prev != status, tx.Commit()
}

// Usage answers the questions the publish limits ask. It counts drafts in
// status publishing or published by their account_id. "Another account"
// always means another account of the same owner: the rules keep one person's
// accounts from looking like several people.
type Usage interface {
	// PublishedSince counts the account's publications since t.
	PublishedSince(accountID int64, since time.Time) (int, error)
	// LastPublished is the account's latest publication (zero if none).
	LastPublished(accountID int64) (time.Time, error)
	// LastPublishedIn is the account's latest publication in a community (subreddit, …), zero if none.
	LastPublishedIn(accountID int64, community string) (time.Time, error)
	// OtherAccountInThread reports whether another account on the platform already replied to replyTo.
	OtherAccountInThread(platform string, accountID int64, replyTo string) (bool, error)
	// OtherAccountInCommunity reports whether another account on the platform published in community since t.
	OtherAccountInCommunity(platform string, accountID int64, community string, since time.Time) (bool, error)

	// PublishedTimes lists the account's publication times since t, oldest first.
	PublishedTimes(accountID int64, since time.Time) ([]time.Time, error)
	// LastPublishedInByOthers is the latest publication in community by
	// another account on the platform, zero if none.
	LastPublishedInByOthers(platform string, accountID int64, community string) (time.Time, error)
	// RemovedFromOtherAccount returns the handle of another account on the
	// platform whose publication of the same reply target, or the same post
	// (community and title), was removed; "" if none.
	RemovedFromOtherAccount(platform string, accountID int64, replyTo, community, title string) (string, error)
}

// publicationAt is when a counted draft was published; a draft being
// published counts from its claim.
const publicationAt = `COALESCE(published_at, updated_at)`

// counted limits a query to drafts that count against the limits.
const counted = `status IN ('` + DraftPublishing + `', '` + DraftPublished + `')`

// normCommunity compares communities without an r/ prefix or case, so
// "r/golang" and "golang" are one subreddit.
const normCommunity = `lower(CASE WHEN lower(community) LIKE 'r/%' THEN substr(community, 3) ELSE community END)`

// NormCommunity is the form communities are compared in.
func NormCommunity(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	return strings.TrimPrefix(c, "r/")
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type usage struct{ q queryer }

// Usage returns a Usage over the whole database. It must not be used inside
// a ClaimForPublish guard, which gets its own transaction-bound Usage.
func (s *Store) Usage() Usage { return usage{s.rdb} }

// sameOwner limits drafts d to those of the owner of the account bound to
// the query's account parameter.
const sameOwner = `user_id IS (SELECT o.user_id FROM accounts AS o WHERE o.id = ?)`

func (u usage) PublishedSince(accountID int64, since time.Time) (int, error) {
	var n int
	err := u.q.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM drafts WHERE account_id = ? AND `+counted+
		` AND `+publicationAt+` >= ?`, accountID, stamp(since)).Scan(&n)
	return n, err
}

func (u usage) PublishedTimes(accountID int64, since time.Time) ([]time.Time, error) {
	rows, err := u.q.QueryContext(context.Background(), `SELECT `+publicationAt+` AS t FROM drafts WHERE account_id = ? AND `+counted+
		` AND `+publicationAt+` >= ? ORDER BY t`, accountID, stamp(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, parseStamp(v))
	}
	return out, rows.Err()
}

func (u usage) last(q string, args ...any) (time.Time, error) {
	var v sql.NullString
	if err := u.q.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		return time.Time{}, err
	}
	if !v.Valid {
		return time.Time{}, nil
	}
	return parseStamp(v.String), nil
}

func (u usage) LastPublished(accountID int64) (time.Time, error) {
	return u.last(`SELECT MAX(`+publicationAt+`) FROM drafts WHERE account_id = ? AND `+counted, accountID)
}

func (u usage) LastPublishedIn(accountID int64, community string) (time.Time, error) {
	return u.last(`SELECT MAX(`+publicationAt+`) FROM drafts WHERE account_id = ? AND `+counted+` AND `+normCommunity+` = ?`,
		accountID, NormCommunity(community))
}

func (u usage) LastPublishedInByOthers(platform string, accountID int64, community string) (time.Time, error) {
	return u.last(`SELECT MAX(`+publicationAt+`) FROM drafts WHERE platform = ? AND account_id IS NOT NULL AND account_id != ? AND `+
		counted+` AND `+normCommunity+` = ? AND `+sameOwner, platform, accountID, NormCommunity(community), accountID)
}

func (u usage) OtherAccountInCommunity(platform string, accountID int64, community string, since time.Time) (bool, error) {
	last, err := u.LastPublishedInByOthers(platform, accountID, community)
	return !last.IsZero() && !last.Before(since), err
}

// OtherAccountInThread also counts a reply to a post another of our accounts
// published: answering yourself from a second account is the same thing.
func (u usage) OtherAccountInThread(platform string, accountID int64, replyTo string) (bool, error) {
	target := strings.TrimRight(strings.TrimSpace(replyTo), "/")
	if target == "" {
		return false, nil
	}
	var n int
	err := u.q.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM drafts WHERE platform = ? AND account_id IS NOT NULL
		AND account_id != ? AND `+counted+` AND (rtrim(reply_to, '/') = ? OR rtrim(remote_url, '/') = ?) AND `+sameOwner,
		platform, accountID, target, target, accountID).Scan(&n)
	return n > 0, err
}

func (u usage) RemovedFromOtherAccount(platform string, accountID int64, replyTo, community, title string) (string, error) {
	q := `SELECT a.handle FROM drafts AS d JOIN accounts AS a ON a.id = d.account_id
		WHERE d.platform = ? AND d.account_id != ? AND d.removed_at IS NOT NULL
		AND d.user_id IS (SELECT o.user_id FROM accounts AS o WHERE o.id = ?) AND `
	args := []any{platform, accountID, accountID}
	switch target := strings.TrimRight(strings.TrimSpace(replyTo), "/"); {
	case target != "":
		q += `rtrim(d.reply_to, '/') = ?`
		args = append(args, target)
	case strings.TrimSpace(title) != "":
		q += `d.reply_to IS NULL AND lower(trim(d.title)) = ? AND COALESCE(` + strings.ReplaceAll(normCommunity, "community", "d.community") + `, '') = ?`
		args = append(args, strings.ToLower(strings.TrimSpace(title)), NormCommunity(community))
	default:
		return "", nil
	}
	var handle string
	err := u.q.QueryRowContext(context.Background(), q+` LIMIT 1`, args...).Scan(&handle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return handle, err
}

// ClaimForPublish is BeginPublish with the publish limits checked in the
// same write transaction: it sets the draft's account, runs guard against
// that transaction and, if guard returns nil, moves the draft from approved
// to publishing. Two publishes can therefore never both pass a limit.
//
// The transaction is BEGIN IMMEDIATE, so a second radaro process waits for
// it too. guard must use only the Usage it is given: the store has a single
// connection, which the claim holds until it returns.
//
// Both the draft and the account must be the user's.
func (s *Store) ClaimForPublish(userID, draftID, accountID int64, guard func(Usage) error) (*Draft, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	d, err := claim(ctx, conn, userID, draftID, accountID, guard)
	conn.Close()
	return d, err
}

func claim(ctx context.Context, conn *sql.Conn, userID, draftID, accountID int64, guard func(Usage) error) (d *Draft, err error) {
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	where, wargs := owner("user_id", userID)
	d, err = scanDraft(conn.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ? AND `+where, append([]any{draftID}, wargs...)...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: draft %d", ErrNotFound, draftID)
	}
	if err != nil {
		return nil, err
	}
	if d.Status != DraftApproved {
		return nil, fmt.Errorf("%w: draft %d is %s", ErrConflict, draftID, d.Status)
	}
	var platform string
	err = conn.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id = ? AND `+where, append([]any{accountID}, wargs...)...).Scan(&platform)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: account %d", ErrNotFound, accountID)
	}
	if err != nil {
		return nil, err
	}
	if platform != d.Platform {
		return nil, fmt.Errorf("account %d is not a %s account", accountID, d.Platform)
	}
	if guard != nil {
		if err = guard(usage{conn}); err != nil {
			return nil, err
		}
	}
	now := stamp(time.Now())
	if _, err = conn.ExecContext(ctx, `UPDATE drafts SET account_id = ?, status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		accountID, DraftPublishing, now, draftID, DraftApproved); err != nil {
		return nil, err
	}
	d, err = scanDraft(conn.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM drafts WHERE id = ?`, draftID))
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, err
	}
	return d, nil
}

// ReturnToApproved puts a claimed draft back to approved with a note, for a
// publish the platform refused before posting anything (a rate limit).
// The draft keeps its account, so a retry never moves it to another one.
func (s *Store) ReturnToApproved(draftID int64, note string) (*Draft, error) {
	return s.transition(0, draftID, DraftApproved, []string{DraftPublishing}, `, error = ?`, nullIfEmpty(truncate(note, 1000)))
}

// MarkRemoved records that a published draft was removed on the platform;
// it reports false if it already was.
func (s *Store) MarkRemoved(draftID int64) (bool, error) {
	now := stamp(time.Now())
	res, err := s.db.Exec(`UPDATE drafts SET removed_at = ?, updated_at = ? WHERE id = ? AND status = ? AND removed_at IS NULL`,
		now, now, draftID, DraftPublished)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	d, err := s.Draft(0, draftID)
	switch {
	case err != nil:
		return false, err
	case d == nil:
		return false, fmt.Errorf("%w: draft %d", ErrNotFound, draftID)
	case d.Status != DraftPublished:
		return false, fmt.Errorf("%w: draft %d is %s", ErrConflict, draftID, d.Status)
	}
	return false, nil
}
