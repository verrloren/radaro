package store

import (
	"database/sql"
	"errors"
)

const proxySettingKey = "outbound_proxy_url"

// ProxyURL is the saved, instance-wide outbound proxy. Empty uses the
// process's HTTP_PROXY/HTTPS_PROXY environment settings.
func (s *Store) ProxyURL() (string, error) {
	var raw string
	err := s.rdb.QueryRow(`SELECT value FROM instance WHERE key = ?`, proxySettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return raw, err
}

// SaveProxyURL replaces the instance-wide proxy. Empty removes the override.
func (s *Store) SaveProxyURL(raw string) error {
	if raw == "" {
		_, err := s.db.Exec(`DELETE FROM instance WHERE key = ?`, proxySettingKey)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO instance (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, proxySettingKey, raw)
	return err
}
