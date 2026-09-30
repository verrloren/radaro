package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withMigrations(t *testing.T, extra ...migration) {
	t.Helper()
	saved := migrations
	migrations = append(append([]migration{}, saved...), extra...)
	t.Cleanup(func() { migrations = saved })
}

func latestVersion() int { return migrations[len(migrations)-1].version }

func userVersion(t *testing.T, st *Store) int {
	t.Helper()
	var v int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationStepsRunOnceAndKeepData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	latest := latestVersion()
	if v := userVersion(t, st); v != latest {
		t.Fatalf("fresh database version = %d, want %d", v, latest)
	}
	if _, err := st.CreateProject(0, "Acme"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	runs := 0
	withMigrations(t, migration{latest + 1, func(tx *sql.Tx) error {
		runs++
		_, err := tx.Exec(`ALTER TABLE projects ADD COLUMN note TEXT`)
		return err
	}})
	for range 2 {
		st, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if v := userVersion(t, st); v != latest+1 {
			t.Fatalf("version = %d, want %d", v, latest+1)
		}
		ps, err := st.Projects(0)
		if err != nil || len(ps) != 2 {
			t.Fatalf("projects after migration = %v, %v", ps, err)
		}
		st.Close()
	}
	if runs != 1 {
		t.Fatalf("step ran %d times, want 1", runs)
	}
	// The database as it was before the upgrade is kept next to it.
	snapshot := fmt.Sprintf("%s.v%d.bak", path, latest)
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pre-upgrade snapshot: %v, %v", info, err)
	}
	db, _ := sql.Open("sqlite", "file:"+snapshot)
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != latest {
		t.Fatalf("snapshot version = %d, %v; want %d", v, err, latest)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	latest := latestVersion()
	withMigrations(t, migration{latest + 1, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE projects ADD COLUMN note TEXT`); err != nil {
			return err
		}
		return errors.New("boom")
	}})
	if _, err := Open(path); err == nil {
		t.Fatal("open succeeded despite a failing migration")
	}
	migrations = migrations[:len(migrations)-1]
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v := userVersion(t, st); v != latest {
		t.Fatalf("version = %d after a failed step, want %d", v, latest)
	}
	if _, err := st.db.Exec(`SELECT note FROM projects`); err == nil {
		t.Fatal("the failed step's column survived the rollback")
	}
	var fk int
	if err := st.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v; want them back on", fk, err)
	}
}

func TestRebuildTableKeepsRowsReferencesAndCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.SaveAccount(0, "devto", "alice", map[string]string{"api_key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := st.SaveAccount(0, "devto", "bob", map[string]string{"api_key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteAccount(0, gone.ID); err != nil {
		t.Fatal(err)
	}
	d, err := st.CreateDraft(NewDraft{Platform: "devto", AccountID: a.ID, Kind: "post", Title: "t", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Add a CHECK constraint, which SQLite cannot ALTER in.
	withMigrations(t, migration{latestVersion() + 1, func(tx *sql.Tx) error {
		return rebuildTable(tx, "accounts", `
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
			platform    TEXT NOT NULL,
			handle      TEXT NOT NULL,
			credentials TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			UNIQUE (user_id, platform, handle),
			CHECK (platform != '')`,
			`id, user_id, platform, handle, credentials, created_at, updated_at`,
			`CREATE INDEX idx_accounts_platform ON accounts(platform)`)
	}})
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Draft(0, d.ID)
	if err != nil || got == nil || got.AccountID == nil || *got.AccountID != a.ID {
		t.Fatalf("draft lost its account after the rebuild: %+v, %v", got, err)
	}
	next, err := st.SaveAccount(0, "devto", "carol", map[string]string{"api_key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID <= gone.ID {
		t.Fatalf("new account reused id %d (deleted id was %d)", next.ID, gone.ID)
	}
	// The draft's reference still points at the rebuilt table.
	if _, err := st.DeleteAccount(0, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Draft(0, d.ID); got.AccountID != nil {
		t.Fatal("ON DELETE SET NULL no longer fires after the rebuild")
	}
}

func TestMigrationRejectsForeignKeyViolations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radaro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	withMigrations(t, migration{latestVersion() + 1, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO drafts (platform, account_id, kind, body, status, created_at, updated_at)
			VALUES ('devto', 999, 'post', 'b', 'review', 'x', 'x')`)
		return err
	}})
	if _, err := Open(path); err == nil {
		t.Fatal("a step that breaks a foreign key was committed")
	}
}

func TestReadsDoNotWaitForTheWriter(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "radaro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('Pending', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	done := make(chan []Project, 1)
	go func() {
		ps, _ := st.Projects(0)
		done <- ps
	}()
	select {
	case ps := <-done:
		if len(ps) != 1 {
			t.Fatalf("reader saw %d projects, want only the committed Default", len(ps))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read blocked behind an open write transaction")
	}
}

func TestReaderPoolRejectsWrites(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "radaro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.rdb.Exec(`DELETE FROM projects`); err == nil {
		t.Fatal("the read pool accepted a write")
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "radaro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateProject(0, "Acme"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup.db")
	if err := st.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if err := st.Backup(context.Background(), dest); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	db, err := sql.Open("sqlite", "file:"+dest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ok string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("integrity_check = %q, %v", ok, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = 'Acme'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup has %d Acme projects, %v", n, err)
	}
	if err := openTest(t).Backup(context.Background(), filepath.Join(dir, "mem.db")); err == nil {
		t.Fatal("backed up an in-memory database")
	}
}
