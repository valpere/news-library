package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// downgradeToV14 turns a fresh v15 database into a v14 one.
func downgradeToV14(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"DROP TABLE completeness_confirmations", "DROP TABLE search_judgments", "DROP TABLE search_events", "DROP VIEW active_items", "DROP TABLE trash_entries", "PRAGMA user_version=14"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenBacksUpBeforeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := add(t, s, "https://example.com/a")
	s.Close()
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), preMigrationDir, "*")); len(m) != 0 {
		t.Fatalf("fresh database must not be backed up: %v", m)
	}
	downgradeToV14(t, path)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), preMigrationDir, "library-v14-*.sqlite"))
	if len(m) != 1 {
		t.Fatalf("want one v14 backup, got %v", m)
	}
	b, err := sql.Open("sqlite", "file:"+m[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var v, n int
	if err := b.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 14 {
		t.Fatalf("backup version %d %v", v, err)
	}
	if err := b.QueryRow("SELECT count(*) FROM items WHERE id=?", id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup lost item: %d %v", n, err)
	}
	s, err = Open(path) // current schema: no new backup
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if m2, _ := filepath.Glob(filepath.Join(filepath.Dir(path), preMigrationDir, "*")); len(m2) != 1 {
		t.Fatalf("no backup expected at current schema: %v", m2)
	}
}

func TestPreMigrationBackupRetention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), preMigrationDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"library-v9-20260101T000000Z.sqlite", "library-v10-20260102T000000Z.sqlite",
		"library-v11-20260103T000000Z.sqlite", "library-v12-20260104T000000Z.sqlite", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := prunePreMigration(dir, 3); err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(m) != 4 { // 3 newest backups + untouched foreign file
		t.Fatalf("got %v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "library-v9-20260101T000000Z.sqlite")); err == nil {
		t.Fatal("oldest backup must be pruned")
	}
}
