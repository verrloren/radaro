package store

import (
	"database/sql"
	"sort"
	"time"
)

// AccountActivity is one account's publishing record.
type AccountActivity struct {
	AccountID       int64   `json:"account_id"`
	Published24h    int     `json:"published_24h"`
	Published7d     int     `json:"published_7d"`
	Published30d    int     `json:"published_30d"`
	Failed30d       int     `json:"failed_30d"`
	Removed30d      int     `json:"removed_30d"` // of the ones published in the last 30 days
	LastPublishedAt *string `json:"last_published_at"`
	LastError       *string `json:"last_error"`
}

// AccountActivity returns the publishing record of every account of the user, by id.
func (s *Store) AccountActivity(userID int64, now time.Time) (map[int64]AccountActivity, error) {
	day, week, month := stamp(now.Add(-24*time.Hour)), stamp(now.Add(-7*24*time.Hour)), stamp(now.Add(-30*24*time.Hour))
	// published_at is only ever set by a successful publish, so it alone
	// marks a publication, whether or not the post was removed later.
	where, wargs := owner("a.user_id", userID)
	rows, err := s.rdb.Query(`SELECT a.id,
		COUNT(CASE WHEN d.published_at >= ?1 THEN 1 END),
		COUNT(CASE WHEN d.published_at >= ?2 THEN 1 END),
		COUNT(CASE WHEN d.published_at >= ?3 THEN 1 END),
		COUNT(CASE WHEN d.status = ?4 AND d.updated_at >= ?3 THEN 1 END),
		COUNT(CASE WHEN d.published_at >= ?3 AND d.removed_at IS NOT NULL THEN 1 END),
		MAX(d.published_at),
		(SELECT f.error FROM drafts f WHERE f.account_id = a.id AND f.status = ?4
			ORDER BY f.updated_at DESC, f.id DESC LIMIT 1)
		FROM accounts a LEFT JOIN drafts d ON d.account_id = a.id
		WHERE `+where+`
		GROUP BY a.id`, append([]any{day, week, month, DraftFailed}, wargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]AccountActivity{}
	for rows.Next() {
		var (
			a            AccountActivity
			last, errMsg sql.NullString
		)
		if err := rows.Scan(&a.AccountID, &a.Published24h, &a.Published7d, &a.Published30d, &a.Failed30d, &a.Removed30d,
			&last, &errMsg); err != nil {
			return nil, err
		}
		a.LastPublishedAt, a.LastError = nullStr(last), nullStr(errMsg)
		out[a.AccountID] = a
	}
	return out, rows.Err()
}

// BanRatePoint is how many of a platform's accounts were dead (invalid or
// suspended) at the end of a day, from account_status_events.
type BanRatePoint struct {
	Day      string `json:"day"` // YYYY-MM-DD, UTC
	Platform string `json:"platform"`
	Total    int    `json:"total"`
	Dead     int    `json:"dead"`
}

// BanRateSeries returns one point per platform per day for the last days
// days, over the user's accounts.
func (s *Store) BanRateSeries(userID int64, days int, now time.Time) ([]BanRatePoint, error) {
	if days < 1 {
		return []BanRatePoint{}, nil
	}
	accounts, names, err := s.banAccounts(userID)
	if err != nil {
		return nil, err
	}
	today := now.UTC().Truncate(24 * time.Hour)
	first := today.AddDate(0, 0, -(days - 1))
	// Every event up to the end of the window: the status on the first day
	// may come from an event long before it.
	if err := s.banEvents(accounts, stamp(today.AddDate(0, 0, 1))); err != nil {
		return nil, err
	}
	out := []BanRatePoint{}
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		out = append(out, banDay(d, accounts, names)...)
	}
	return out, nil
}

// banAccount is an account with its status events, sorted by time.
type banAccount struct {
	platform, created string
	events            []banEvent
}

// banAccounts loads the user's accounts and the sorted names of their platforms.
func (s *Store) banAccounts(userID int64) (map[int64]*banAccount, []string, error) {
	where, args := owner("user_id", userID)
	rows, err := s.rdb.Query(`SELECT id, platform, created_at FROM accounts WHERE `+where, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	accounts := map[int64]*banAccount{}
	platforms := map[string]bool{}
	for rows.Next() {
		var id int64
		a := &banAccount{}
		if err := rows.Scan(&id, &a.platform, &a.created); err != nil {
			return nil, nil, err
		}
		accounts[id] = a
		platforms[a.platform] = true
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(platforms))
	for p := range platforms {
		names = append(names, p)
	}
	sort.Strings(names)
	return accounts, names, nil
}

// banEvents attaches the status events before end to their accounts.
func (s *Store) banEvents(accounts map[int64]*banAccount, end string) error {
	rows, err := s.rdb.Query(`SELECT account_id, at, status FROM account_status_events WHERE at < ? ORDER BY at, id`, end)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			e  banEvent
		)
		if err := rows.Scan(&id, &e.at, &e.status); err != nil {
			return err
		}
		if a := accounts[id]; a != nil {
			a.events = append(a.events, e)
		}
	}
	return rows.Err()
}

// banDay is one point per platform for day d, counting the accounts that
// existed by its end.
func banDay(d time.Time, accounts map[int64]*banAccount, names []string) []BanRatePoint {
	dayEnd := stamp(d.AddDate(0, 0, 1)) // events strictly before the next midnight
	total, dead := map[string]int{}, map[string]int{}
	for _, a := range accounts {
		if a.created >= dayEnd {
			continue
		}
		total[a.platform]++
		if banDead(banStatusAt(a.events, dayEnd)) {
			dead[a.platform]++
		}
	}
	day := d.Format("2006-01-02")
	out := make([]BanRatePoint, 0, len(names))
	for _, p := range names {
		out = append(out, BanRatePoint{Day: day, Platform: p, Total: total[p], Dead: dead[p]})
	}
	return out
}

type banEvent struct{ at, status string }

// banStatusAt returns the status of the latest event before t, or "" if
// there is none; events are sorted by time.
func banStatusAt(events []banEvent, t string) string {
	i := sort.Search(len(events), func(i int) bool { return events[i].at >= t })
	if i == 0 {
		return ""
	}
	return events[i-1].status
}

func banDead(status string) bool { return status == AccountInvalid || status == AccountSuspended }
