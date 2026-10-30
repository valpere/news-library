package library

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// PurgeBlockedError says why a trashed material cannot be erased: something that
// must stay immutable still points at it.
type PurgeBlockedError struct{ Why string }

func (e *PurgeBlockedError) Error() string { return "не можна видалити: " + e.Why }

// PurgeBlocker returns why a trashed material cannot be erased, or "" when it can.
// Connection notes keep immutable revisions with exact references, so a material
// they mention stays in the trash.
func (s *Store) PurgeBlocker(id int64) (string, error) {
	var notes, running int
	if err := s.DB.QueryRow(`SELECT (SELECT count(DISTINCT version_id) FROM connection_members WHERE item_id=?),
 (SELECT count(*) FROM jobs WHERE item_id=? AND state='running')`, id, id).Scan(&notes, &running); err != nil {
		return "", err
	}
	switch {
	case notes > 0:
		return fmt.Sprintf("матеріал згадано в нотатках-зв'язках (редакцій: %d), їхня історія незмінна", notes), nil
	case running > 0:
		return "матеріал зараз обробляється; повторіть пізніше", nil
	}
	return "", nil
}

// purgeFileColumns lists every place that references a stored file by path.
var purgeFileColumns = [][2]string{
	{"texts", "text_path"}, {"digests", "markdown_path"}, {"digest_checkpoints", "markdown_path"},
	{"article_knowledge", "markdown_path"}, {"job_failures", "response_path"}, {"knowledge_failures", "response_path"},
	{"connection_versions", "markdown_path"}, {"cache_entries", "body_path"},
}

// PurgeTrashed erases a trashed material for good: every row of its own history
// (texts, digests, ratings, jobs, knowledge, search verdicts) and the files nothing
// else uses. It refuses a material that is not in the trash (sql.ErrNoRows) or that
// immutable records still mention (*PurgeBlockedError). All rows go in one
// transaction, so a failure leaves the material whole; files are removed after it.
func (s *Store) PurgeTrashed(id int64) error {
	if n := 0; s.DB.QueryRow(`SELECT count(*) FROM trash_entries WHERE item_id=?`, id).Scan(&n) != nil || n == 0 {
		return sql.ErrNoRows
	}
	why, err := s.PurgeBlocker(id)
	if err != nil {
		return err
	}
	if why != "" {
		return &PurgeBlockedError{why}
	}
	var original, canonical string
	if err = s.DB.QueryRow(`SELECT original_url,canonical_url FROM items WHERE id=?`, id).Scan(&original, &canonical); err != nil {
		return err
	}
	paths := map[string]bool{}
	collect := func(query string, args ...any) error {
		rows, e := s.DB.Query(query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				return e
			}
			if p != "" {
				paths[p] = true
			}
		}
		return rows.Err()
	}
	for _, q := range []string{
		`SELECT text_path FROM texts WHERE item_id=?`,
		`SELECT markdown_path FROM digests WHERE item_id=?`,
		`SELECT markdown_path FROM digest_checkpoints WHERE job_id IN (SELECT id FROM jobs WHERE item_id=?)`,
		`SELECT markdown_path FROM article_knowledge WHERE item_id=?`,
		`SELECT response_path FROM job_failures WHERE job_id IN (SELECT id FROM jobs WHERE item_id=?)`,
		`SELECT response_path FROM knowledge_failures WHERE run_id IN (SELECT id FROM knowledge_runs WHERE item_id=?)`,
	} {
		if err = collect(q, id); err != nil {
			return err
		}
	}
	if err = collect(`SELECT body_path FROM cache_entries WHERE url IN (?,?)`, original, canonical); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobs := `(SELECT id FROM jobs WHERE item_id=?)`
	runs := `(SELECT id FROM knowledge_runs WHERE item_id=?)`
	for _, q := range []string{
		`DELETE FROM embedding_chunks WHERE item_id=?`,
		`DELETE FROM embedding_runs WHERE item_id=?`,
		`DELETE FROM knowledge_failures WHERE run_id IN ` + runs,
		`DELETE FROM article_knowledge WHERE item_id=?`,
		`DELETE FROM knowledge_runs WHERE item_id=?`,
		`DELETE FROM interest_predictions WHERE item_id=?`,
		`DELETE FROM digest_feedback WHERE item_id=?`,
		`DELETE FROM digest_checkpoints WHERE job_id IN ` + jobs,
		`DELETE FROM job_failures WHERE job_id IN ` + jobs,
		`DELETE FROM filter_decisions WHERE item_id=?`,
		`DELETE FROM search_judgments WHERE item_id=?`,
		`DELETE FROM completeness_confirmations WHERE item_id=?`,
		`DELETE FROM digests WHERE item_id=?`,
		`DELETE FROM jobs WHERE item_id=?`,
		`DELETE FROM texts WHERE item_id=?`,
		`DELETE FROM item_tags WHERE item_id=?`,
		`DELETE FROM source_aliases WHERE item_id=?`,
		`DELETE FROM trash_entries WHERE item_id=?`,
		`DELETE FROM items WHERE id=?`,
	} {
		if _, err = tx.Exec(q, id); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	if _, err = tx.Exec(`DELETE FROM cache_entries WHERE url IN (?,?)`, original, canonical); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.removeUnusedFiles(paths)
}

// removeUnusedFiles deletes content-addressed files that no remaining row uses:
// two materials with the same text share one file.
func (s *Store) removeUnusedFiles(paths map[string]bool) error {
	var first error
	for p := range paths {
		used := false
		for _, c := range purgeFileColumns {
			var n int
			if err := s.DB.QueryRow(`SELECT count(*) FROM `+c[0]+` WHERE `+c[1]+`=?`, p).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				used = true
				break
			}
		}
		if used {
			continue
		}
		clean := filepath.Clean(p)
		if filepath.IsAbs(clean) || clean == ".." || len(clean) > 2 && clean[:3] == ".."+string(filepath.Separator) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Files.Root, clean)); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = fmt.Errorf("рядки видалено, але файл %s лишився: %w", p, err)
		}
	}
	return first
}

// PurgeAllTrashed erases every trashed material it may and counts the ones that
// stay because immutable records still mention them.
func (s *Store) PurgeAllTrashed() (purged, kept int, err error) {
	rows, err := s.DB.Query(`SELECT item_id FROM trash_entries ORDER BY item_id`)
	if err != nil {
		return 0, 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var blocked *PurgeBlockedError
		switch e := s.PurgeTrashed(id); {
		case e == nil:
			purged++
		case errors.As(e, &blocked):
			kept++
		default:
			return purged, kept, e
		}
	}
	return purged, kept, nil
}

func (w *Web) purgeTrashed(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if r.PostForm.Get("confirm") != "1" {
		http.Error(rw, "підтвердіть видалення назавжди", http.StatusBadRequest)
		return
	}
	var blocked *PurgeBlockedError
	switch err = w.Store.PurgeTrashed(id); {
	case err == nil:
		http.Redirect(rw, r, "/trash", http.StatusSeeOther)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(rw, r)
	case errors.As(err, &blocked):
		http.Error(rw, blocked.Error(), http.StatusConflict)
	default:
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func (w *Web) clearTrash(rw http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "1" {
		http.Error(rw, "підтвердіть видалення назавжди", http.StatusBadRequest)
		return
	}
	purged, kept, err := w.Store.PurgeAllTrashed()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(rw, r, "/trash?purged="+strconv.Itoa(purged)+"&kept="+strconv.Itoa(kept), http.StatusSeeOther)
}
