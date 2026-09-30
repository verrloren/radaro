package store

import (
	"encoding/json"
	"os"
	"time"
)

// SourceSettings returns the source credentials saved from the dashboard,
// keyed by source then field.
func (s *Store) SourceSettings() (map[string]map[string]string, error) {
	rows, err := s.rdb.Query(`SELECT source, settings FROM source_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var source, raw string
		if err := rows.Scan(&source, &raw); err != nil {
			return nil, err
		}
		values := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		out[source] = values
	}
	return out, rows.Err()
}

// SaveSourceSettings replaces the saved settings for one source.
func (s *Store) SaveSourceSettings(source string, values map[string]string) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO source_settings (source, settings, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET settings = excluded.settings, updated_at = excluded.updated_at`,
		source, string(raw), stamp(time.Now()))
	if err != nil {
		return err
	}
	s.restrict()
	return nil
}

// restrict makes the database private to this user once it holds credentials.
func (s *Store) restrict() {
	if s.path == ":memory:" {
		return
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(s.path+suffix, 0o600)
	}
}

// DeleteSourceSettings forgets one source's saved settings, falling back to the environment.
func (s *Store) DeleteSourceSettings(source string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM source_settings WHERE source = ?`, source)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
