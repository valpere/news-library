package library

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type primaryTable struct {
	table, column, trigger string
}

// One inventory covers backups and path-only relocation, including diagnostics.
func primaryTables(schema int) []primaryTable {
	if schema < 3 {
		return nil // Text still lives in SQLite until the normal schema migration.
	}
	refs := []primaryTable{
		{"source_versions", "text_path", "immutable_snapshot_update"},
		{"generated_artifacts", "markdown_path", "immutable_artifact_update"},
	}
	if schema >= 5 {
		refs = append(refs, primaryTable{"digest_checkpoints", "markdown_path", "immutable_checkpoint_update"})
	}
	if schema >= 8 {
		refs = append(refs, primaryTable{"delivery_parts", "text_path", ""})
	}
	if schema >= 9 {
		refs = append(refs, primaryTable{"connection_versions", "markdown_path", "immutable_connection_version_update"})
	}
	if schema >= 10 {
		refs = append(refs, primaryTable{"job_failures", "response_path", "immutable_job_failure_update"})
	}
	if schema >= 11 {
		refs = append(refs, primaryTable{"article_knowledge", "markdown_path", "immutable_article_knowledge_update"}, primaryTable{"knowledge_failures", "response_path", "immutable_knowledge_failure_update"})
	}
	return refs
}

// Called only on an exclusive, newly restored directory behind the transfer
// marker. Copy first, publish references atomically, then remove flat copies.
func repackCopiedFiles(ctx context.Context, root string, schema int) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "library.sqlite")}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	refs, err := primaryRefs(db, schema)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(refs))
	for path := range refs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	moves := map[string]string{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := blobPath(strings.Split(path, string(filepath.Separator))[0], refs[path])
		if err != nil {
			return err
		}
		if target == path {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, target)); err == nil {
			regular, err := regularPath(root, target)
			if err != nil {
				return err
			}
			hash, _, err := hashFile(regular)
			if err != nil {
				return err
			}
			if hash != refs[path] {
				return errors.New("repack target hash mismatch")
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if _, err := transferFileAs(root, root, path, target, refs[path]); err != nil {
				return err
			}
		} else {
			return err
		}
		moves[path] = target
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE repack_paths(old TEXT PRIMARY KEY, new TEXT NOT NULL)"); err != nil {
		return err
	}
	for _, path := range paths {
		if target, ok := moves[path]; ok {
			if _, err := tx.ExecContext(ctx, "INSERT INTO repack_paths VALUES(?,?)", path, target); err != nil {
				return err
			}
		}
	}
	for _, ref := range primaryTables(schema) {
		var triggerSQL string
		if ref.trigger != "" {
			if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?", ref.trigger).Scan(&triggerSQL); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+ref.trigger); err != nil {
				return err
			}
		}
		statement := "UPDATE " + ref.table + " SET " + ref.column + "=(SELECT new FROM repack_paths WHERE old=" + ref.column + ") WHERE " + ref.column + " IN (SELECT old FROM repack_paths)"
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
		if triggerSQL != "" {
			if _, err := tx.ExecContext(ctx, triggerSQL); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	for _, path := range paths {
		if _, ok := moves[path]; ok {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(root, path)); err != nil {
				return err
			}
			if err := syncDirectory(filepath.Dir(filepath.Join(root, path))); err != nil {
				return err
			}
		}
	}
	return nil
}
