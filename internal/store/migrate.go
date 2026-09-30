package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// migration brings the schema up to version inside one transaction.
type migration struct {
	version int
	up      func(tx *sql.Tx) error
}

// migrations is append-only: a released step never changes, a new change is a
// new step with the next version.
var migrations = []migration{
	// Versions 1–3 only ever added tables, so one idempotent step covers them.
	{3, baseline},
	{4, usersAndSessions},
	{5, ownership},
}

func baseline(tx *sql.Tx) error {
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	now := stamp(time.Now())
	_, err := tx.Exec(`INSERT OR IGNORE INTO projects (id, name, created_at, updated_at) VALUES (?, 'Default', ?, ?)`,
		DefaultProjectID, now, now)
	return err
}

func usersAndSessions(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    email         TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE TABLE refresh_tokens (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BLOB NOT NULL UNIQUE,
    expires_at  TEXT NOT NULL,
    revoked_at  TEXT,
    replaced_by INTEGER,
    created_at  TEXT NOT NULL,
    user_agent  TEXT
);
CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
CREATE TABLE instance (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`)
	return err
}

// ownership gives projects, accounts, drafts and the activity log an owner.
// Existing rows stay unowned (NULL) until the first user registers and
// takes them over.
func ownership(tx *sql.Tx) error {
	if err := rebuildTable(tx, "projects", `
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL COLLATE NOCASE,
    is_default INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (user_id, name)`,
		`id, NULL, name, id = 1, created_at, updated_at`); err != nil {
		return err
	}
	if err := rebuildTable(tx, "accounts", `
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
    platform    TEXT NOT NULL,
    handle      TEXT NOT NULL,
    credentials TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (user_id, platform, handle)`,
		`id, NULL, platform, handle, credentials, created_at, updated_at`); err != nil {
		return err
	}
	_, err := tx.Exec(`
ALTER TABLE drafts ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE activity ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX idx_drafts_user ON drafts(user_id, status, created_at);
CREATE INDEX idx_activity_user ON activity(user_id, id);`)
	return err
}

// migrate applies every step above the database's user_version.
func (s *Store) migrate() (err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	var pending []migration
	for _, m := range migrations {
		if m.version > version {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	// SQLite ignores foreign_keys inside a transaction, and rebuilding a table
	// needs them off; each step checks them itself before committing.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() {
		if _, e := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); e != nil && err == nil {
			err = e
		}
	}()
	for _, m := range pending {
		if err := applyMigration(ctx, conn, m); err != nil {
			return fmt.Errorf("migrate schema to version %d: %w", m.version, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.up(tx); err != nil {
		return err
	}
	rows, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	var table string
	violated := rows.Next()
	if violated {
		var rowid sql.NullInt64
		var parent string
		var fkid int
		err = rows.Scan(&table, &rowid, &parent, &fkid)
	}
	rows.Close()
	if err != nil {
		return err
	}
	if violated {
		return fmt.Errorf("foreign key violation in %s", table)
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// rebuildTable replaces table with a new definition, the only way SQLite can
// change a table's constraints. columns is the new CREATE TABLE body and copy
// the SELECT list that maps old rows onto it. Indexes go with the old table,
// so after recreates them. The AUTOINCREMENT counter is carried over so ids
// of deleted rows are never reused.
func rebuildTable(tx *sql.Tx, table, columns, copy string, after ...string) error {
	var seq sql.NullInt64
	err := tx.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = ?`, table).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tmp := table + "__new"
	for _, q := range []string{
		"CREATE TABLE " + tmp + " (" + columns + ")",
		"INSERT INTO " + tmp + " SELECT " + copy + " FROM " + table,
		"DROP TABLE " + table,
		"ALTER TABLE " + tmp + " RENAME TO " + table,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if seq.Valid {
		res, err := tx.Exec(`UPDATE sqlite_sequence SET seq = max(seq, ?) WHERE name = ?`, seq.Int64, table)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(`INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)`, table, seq.Int64); err != nil {
				return err
			}
		}
	}
	for _, q := range after {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Backup writes a consistent snapshot of the database to dest, which must not
// exist yet. It is safe while other processes use the database.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if s.path == ":memory:" {
		return errors.New("an in-memory database cannot be backed up")
	}
	// Create the file first so the snapshot, which holds credentials, is
	// never readable by others; VACUUM INTO accepts an empty file.
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	// Its own connection: the read pool is query_only, which VACUUM INTO
	// rejects, and the writer must stay free while the snapshot is taken.
	db, err := sql.Open("sqlite", "file:"+s.path+"?_pragma=busy_timeout(30000)")
	if err == nil {
		_, err = db.ExecContext(ctx, `VACUUM INTO ?`, dest)
		db.Close()
	}
	if err != nil {
		os.Remove(dest)
	}
	return err
}
