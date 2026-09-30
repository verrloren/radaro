package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/verrloren/radaro/internal/model"
)

// EnqueueAlerts adds mentions to the durable delivery outbox for one target.
func (s *Store) EnqueueAlerts(mentions []*model.Mention, targetKey string) (int, error) {
	now := stamp(time.Now())
	queued := 0
	for _, m := range mentions {
		res, err := s.db.Exec(`INSERT OR IGNORE INTO alert_outbox (query, mention_id, target_key, enqueued_at) VALUES (?, ?, ?, ?)`,
			m.Query, m.ID, targetKey, now)
		if err != nil {
			return queued, err
		}
		n, _ := res.RowsAffected()
		queued += int(n)
	}
	return queued, nil
}

// PendingAlerts returns undelivered mentions for a target, oldest enqueued first.
func (s *Store) PendingAlerts(query, targetKey string, limit int) ([]*model.Mention, error) {
	rows, err := s.rdb.Query(`SELECT a.mention_id FROM alert_outbox AS a
		JOIN mentions AS m ON m.query = a.query AND m.id = a.mention_id
		WHERE a.query = ? AND a.target_key = ? AND a.delivered_at IS NULL
		ORDER BY a.enqueued_at, a.mention_id LIMIT ?`, query, targetKey, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, rows.Err()
	}
	all, err := s.Mentions(MentionFilter{Scope: Scope{Query: query}})
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Mention{}
	for _, m := range all {
		byID[m.ID] = m
	}
	out := make([]*model.Mention, 0, len(ids))
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// MarkAlerts records one delivery attempt for a batch; a failure keeps it pending.
func (s *Store) MarkAlerts(query string, ids []string, targetKey string, deliveryErr error) error {
	var delivered, lastErr any
	if deliveryErr == nil {
		delivered = stamp(time.Now())
	} else {
		lastErr = truncate(deliveryErr.Error(), 500)
	}
	for start := 0; start < len(ids); start += 900 {
		chunk := ids[start:min(len(ids), start+900)]
		args := []any{delivered, lastErr, query, targetKey}
		for _, id := range chunk {
			args = append(args, id)
		}
		if _, err := s.db.Exec(`UPDATE alert_outbox SET attempts = attempts + 1, delivered_at = ?, last_error = ?
			WHERE query = ? AND target_key = ? AND delivered_at IS NULL AND mention_id IN (`+placeholders(len(chunk))+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// PendingAlertCount counts undelivered mention alerts for a target.
func (s *Store) PendingAlertCount(query, targetKey string) (int, error) {
	var n int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM alert_outbox WHERE query = ? AND target_key = ? AND delivered_at IS NULL`,
		query, targetKey).Scan(&n)
	return n, err
}

// AlertMetrics compares the current window with the preceding complete windows.
type AlertMetrics struct {
	CurrentCount         int
	BaselineCount        int
	BaselineAverage      float64
	CurrentNetSentiment  *float64
	BaselineNetSentiment *float64
}

// AlertMetrics computes volume and net sentiment for the current window and baseline.
func (s *Store) AlertMetrics(query string, now time.Time, windowHours, baselineWindows int) (AlertMetrics, error) {
	if windowHours < 1 || baselineWindows < 1 {
		return AlertMetrics{}, errors.New("alert windows must be at least 1")
	}
	window := time.Duration(windowHours) * time.Hour
	currentStart := now.Add(-window)
	baselineStart := currentStart.Add(-window * time.Duration(baselineWindows))
	rows, err := s.rdb.Query(`SELECT created_at, sentiment FROM mentions WHERE query = ? AND created_at >= ? AND created_at <= ?`,
		query, stamp(baselineStart), stamp(now))
	if err != nil {
		return AlertMetrics{}, err
	}
	defer rows.Close()
	var current, baseline []string
	cur := stamp(currentStart)
	for rows.Next() {
		var created string
		var sent sql.NullString
		if err := rows.Scan(&created, &sent); err != nil {
			return AlertMetrics{}, err
		}
		if created >= cur {
			current = append(current, sent.String)
		} else {
			baseline = append(baseline, sent.String)
		}
	}
	return AlertMetrics{
		CurrentCount:         len(current),
		BaselineCount:        len(baseline),
		BaselineAverage:      float64(len(baseline)) / float64(baselineWindows),
		CurrentNetSentiment:  netOf(current),
		BaselineNetSentiment: netOf(baseline),
	}, rows.Err()
}

func netOf(labels []string) *float64 {
	if len(labels) == 0 {
		return nil
	}
	pos, neg := 0, 0
	for _, l := range labels {
		switch l {
		case string(model.Positive):
			pos++
		case string(model.Negative):
			neg++
		}
	}
	v := roundTo(float64(pos-neg)/float64(len(labels)), 3)
	return &v
}

// ThresholdAlert is one persisted volume/sentiment alert episode.
type ThresholdAlert struct {
	ID      int64
	Text    string
	Payload map[string]any
}

// ActivateThresholdAlert opens an episode, or reuses the active one without
// duplicating it. A cooldown suppresses re-alerting soon after a delivery.
func (s *Store) ActivateThresholdAlert(query, eventType, targetKey, text string, payload map[string]any, cooldownHours int, now time.Time) error {
	body, _ := json.Marshal(payload)
	var id int64
	var delivered sql.NullString
	err := s.db.QueryRow(`SELECT id, delivered_at FROM threshold_alerts
		WHERE query = ? AND event_type = ? AND target_key = ? AND cleared_at IS NULL`, query, eventType, targetKey).Scan(&id, &delivered)
	if err == nil {
		if !delivered.Valid {
			_, err = s.db.Exec(`UPDATE threshold_alerts SET text = ?, payload = ? WHERE id = ?`, text, string(body), id)
		}
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if cooldownHours > 0 {
		var last sql.NullString
		if err := s.db.QueryRow(`SELECT MAX(delivered_at) FROM threshold_alerts WHERE query = ? AND event_type = ? AND target_key = ?`,
			query, eventType, targetKey).Scan(&last); err != nil {
			return err
		}
		if last.Valid && now.Sub(parseStamp(last.String)) < time.Duration(cooldownHours)*time.Hour {
			return nil
		}
	}
	_, err = s.db.Exec(`INSERT INTO threshold_alerts (query, event_type, target_key, text, payload, triggered_at) VALUES (?, ?, ?, ?, ?, ?)`,
		query, eventType, targetKey, text, string(body), stamp(now))
	return err
}

// ClearThresholdAlert closes an active episode so a future crossing can re-arm it.
func (s *Store) ClearThresholdAlert(query, eventType, targetKey string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE threshold_alerts SET cleared_at = ?
		WHERE query = ? AND event_type = ? AND target_key = ? AND cleared_at IS NULL`, stamp(now), query, eventType, targetKey)
	return err
}

// PendingThresholdAlerts lists active, undelivered episodes for a target.
func (s *Store) PendingThresholdAlerts(query, targetKey string) ([]ThresholdAlert, error) {
	rows, err := s.rdb.Query(`SELECT id, text, payload FROM threshold_alerts
		WHERE query = ? AND target_key = ? AND delivered_at IS NULL AND cleared_at IS NULL
		ORDER BY triggered_at, id`, query, targetKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThresholdAlert
	for rows.Next() {
		var a ThresholdAlert
		var body string
		if err := rows.Scan(&a.ID, &a.Text, &body); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(body), &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkThresholdAlert records one delivery attempt; a failure keeps it pending.
func (s *Store) MarkThresholdAlert(id int64, deliveryErr error) error {
	var delivered, lastErr any
	if deliveryErr == nil {
		delivered = stamp(time.Now())
	} else {
		lastErr = truncate(deliveryErr.Error(), 500)
	}
	_, err := s.db.Exec(`UPDATE threshold_alerts SET attempts = attempts + 1, delivered_at = ?, last_error = ?
		WHERE id = ? AND delivered_at IS NULL AND cleared_at IS NULL`, delivered, lastErr, id)
	return err
}

// ThresholdAlertPendingCount counts active undelivered episodes for a target.
func (s *Store) ThresholdAlertPendingCount(query, targetKey string) (int, error) {
	var n int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM threshold_alerts
		WHERE query = ? AND target_key = ? AND delivered_at IS NULL AND cleared_at IS NULL`, query, targetKey).Scan(&n)
	return n, err
}
