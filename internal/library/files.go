package library

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Files struct{ Root string }

func (f Files) Put(kind string, data []byte) (string, error) {
	var extension string
	switch kind {
	case "texts":
		extension = ".txt"
	case "digests":
		extension = ".md"
	case "cache/http":
		extension = ".body"
	default:
		return "", errors.New("invalid blob kind")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	relative := filepath.Join(kind, hash+extension)
	path := filepath.Join(f.Root, relative)
	if _, err := os.Stat(path); err == nil {
		_, err = f.Read(relative, hash)
		return relative, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return "", err
	}
	return relative, nil
}
func (f Files) Read(relative, hash string) ([]byte, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, "..") || !(strings.HasPrefix(clean, "texts"+string(filepath.Separator)) || strings.HasPrefix(clean, "digests"+string(filepath.Separator)) || strings.HasPrefix(clean, "cache/http"+string(filepath.Separator))) {
		return nil, errors.New("invalid stored file path")
	}
	data, err := os.ReadFile(filepath.Join(f.Root, clean))
	if err != nil {
		return nil, errors.New("збережений файл недоступний; перевірте каталог даних або резервну копію")
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
		return nil, errors.New("збережений файл пошкоджено (hash mismatch)")
	}
	return data, nil
}

// File writes are synced before the transaction publishes their references.
// A rollback can leave an unreferenced blob, never a committed missing write.
func migrateFiles(tx *sql.Tx, f Files) error {
	for _, statement := range []string{
		"DROP TRIGGER immutable_snapshot_update", "DROP TRIGGER immutable_artifact_update",
		"ALTER TABLE source_versions ADD COLUMN text_path TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE generated_artifacts ADD COLUMN markdown_path TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE generated_artifacts ADD COLUMN hash TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE jobs ADD COLUMN refresh INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	for _, table := range []struct{ name, column, kind, pathColumn string }{{"source_versions", "text", "texts", "text_path"}, {"generated_artifacts", "markdown", "digests", "markdown_path"}} {
		rows, err := tx.Query("SELECT id," + table.column + " FROM " + table.name)
		if err != nil {
			return err
		}
		type record struct {
			id   int64
			data string
		}
		var records []record
		for rows.Next() {
			var r record
			if err = rows.Scan(&r.id, &r.data); err != nil {
				rows.Close()
				return err
			}
			records = append(records, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, r := range records {
			path, err := f.Put(table.kind, []byte(r.data))
			if err != nil {
				return err
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(r.data)))
			if _, err = tx.Exec("UPDATE "+table.name+" SET "+table.pathColumn+"=?,hash=? WHERE id=?", path, hash, r.id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("ALTER TABLE " + table.name + " DROP COLUMN " + table.column); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		"CREATE TRIGGER immutable_snapshot_update BEFORE UPDATE ON source_versions BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END",
		"CREATE TRIGGER immutable_artifact_update BEFORE UPDATE ON generated_artifacts BEGIN SELECT RAISE(ABORT,'immutable artifact'); END",
		"DROP TABLE item_fts",
		"CREATE VIRTUAL TABLE item_fts USING fts5(title,text,url,content='',contentless_delete=1,tokenize='unicode61')",
		`CREATE TABLE cache_entries (url TEXT PRIMARY KEY,final_url TEXT NOT NULL,content_type TEXT NOT NULL,body_path TEXT NOT NULL,hash TEXT NOT NULL,fetched_at TEXT NOT NULL,expires_at INTEGER NOT NULL,partial INTEGER NOT NULL)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT i.id,i.title,i.canonical_url,v.text_path,v.hash FROM items i LEFT JOIN source_versions v ON v.id=i.selected_snapshot`)
	if err != nil {
		return err
	}
	type entry struct {
		id         int64
		title, url string
		path, hash sql.NullString
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.id, &e.title, &e.url, &e.path, &e.hash); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		var data []byte
		if e.path.Valid {
			data, err = f.Read(e.path.String, e.hash.String)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO item_fts(rowid,title,text,url) VALUES(?,?,?,?)", e.id, e.title, string(data), e.url); err != nil {
			return err
		}
	}
	_, err = tx.Exec("PRAGMA user_version=3")
	return err
}
