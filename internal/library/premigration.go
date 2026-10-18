package library

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	preMigrationDir  = "pre-migration"
	preMigrationKeep = 3
)

// backupBeforeMigration copies the database (DB only, not primary files) to
// pre-migration/library-v<from>-<UTC time>.sqlite before an upgrade from a
// non-empty older schema, then keeps the newest preMigrationKeep copies.
// An error aborts Open so the schema is never changed without a rollback copy.
func backupBeforeMigration(db *sql.DB, path string, from int) error {
	dir := filepath.Join(filepath.Dir(path), preMigrationDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("library-v%d-%s.sqlite", from, time.Now().UTC().Format("20060102T150405Z"))
	if _, err := db.Exec("VACUUM INTO ?", filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("pre-migration backup: %w", err)
	}
	return prunePreMigration(dir, preMigrationKeep)
}

// prunePreMigration removes all but the newest keep backups; names are
// ordered by their trailing timestamp, other files are left alone.
func prunePreMigration(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, "library-v") && strings.HasSuffix(n, ".sqlite") {
			names = append(names, n)
		}
	}
	stamp := func(n string) string { return n[strings.LastIndex(n, "-")+1:] }
	sort.Slice(names, func(i, j int) bool { return stamp(names[i]) < stamp(names[j]) })
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
