package library

import (
	"database/sql"
	"fmt"
	"strings"
)

// installText makes snap the item's only text. When it replaces a different text,
// the rating and everything derived from the old one (digest, knowledge, embeddings,
// filter decision, completeness confirmation, digest jobs) are erased. Identical
// text only refreshes the metadata (changed=false). It returns the file paths the
// erased rows used; the caller removes the ones nothing else uses after the
// transaction commits.
func (s *Store) installText(tx *sql.Tx, itemID int64, snap Snapshot, refreshOnly bool) (changed bool, paths map[string]bool, err error) {
	var oldHash, oldPath string
	err = tx.QueryRow(`SELECT hash,text_path FROM texts WHERE item_id=?`, itemID).Scan(&oldHash, &oldPath)
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return false, nil, err
	}
	retrieved := snap.RetrievedAt
	if retrieved == "" {
		retrieved = nowUTC()
	}
	hash := textHash(snap.Text)
	if exists && oldHash == hash {
		if refreshOnly {
			_, err = tx.Exec(`UPDATE texts SET retrieved_at=?,extractor=?,completeness=?,warning=? WHERE item_id=?`, retrieved, snap.Extractor, snap.Completeness, snap.Warning, itemID)
		}
		return false, nil, err
	}
	if exists {
		if paths, err = s.eraseDerived(tx, itemID); err != nil {
			return false, nil, err
		}
		if _, err = tx.Exec(`UPDATE items SET rating=NULL,rated_at=NULL WHERE id=?`, itemID); err != nil {
			return false, nil, err
		}
		paths[oldPath] = true
	}
	path, err := s.Files.Put("texts", []byte(snap.Text))
	if err != nil {
		return false, nil, err
	}
	_, err = tx.Exec(`INSERT INTO texts(item_id,text_path,title,hash,retrieved_at,origin,extractor,completeness,warning) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(item_id) DO UPDATE SET text_path=excluded.text_path,title=excluded.title,hash=excluded.hash,retrieved_at=excluded.retrieved_at,origin=excluded.origin,extractor=excluded.extractor,completeness=excluded.completeness,warning=excluded.warning`,
		itemID, path, snap.Title, hash, retrieved, snap.Origin, snap.Extractor, snap.Completeness, snap.Warning)
	return true, paths, err
}

// eraseDerived deletes everything produced from the item's current text: the
// digest and its prediction and feedback, knowledge, embeddings, source-stage filter
// decisions, completeness confirmations and the digest jobs with their checkpoints
// and failures. The text row itself and the item stay.
func (s *Store) eraseDerived(tx *sql.Tx, itemID int64) (map[string]bool, error) {
	paths := map[string]bool{}
	for _, q := range []string{
		`SELECT markdown_path FROM digests WHERE item_id=?`,
		`SELECT markdown_path FROM article_knowledge WHERE item_id=?`,
		`SELECT markdown_path FROM digest_checkpoints WHERE job_id IN (SELECT id FROM jobs WHERE item_id=? AND stage='digest')`,
		`SELECT response_path FROM job_failures WHERE job_id IN (SELECT id FROM jobs WHERE item_id=? AND stage='digest')`,
		`SELECT response_path FROM knowledge_failures WHERE run_id IN (SELECT id FROM knowledge_runs WHERE item_id=?)`,
	} {
		rows, err := tx.Query(q, itemID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p string
			if err = rows.Scan(&p); err != nil {
				rows.Close()
				return nil, err
			}
			if p != "" {
				paths[p] = true
			}
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	for _, q := range []string{
		`DELETE FROM embedding_chunks WHERE item_id=?`,
		`DELETE FROM embedding_runs WHERE item_id=?`,
		`DELETE FROM knowledge_failures WHERE run_id IN (SELECT id FROM knowledge_runs WHERE item_id=?)`,
		`DELETE FROM article_knowledge WHERE item_id=?`,
		`DELETE FROM knowledge_runs WHERE item_id=?`,
		`DELETE FROM interest_predictions WHERE item_id=?`,
		`DELETE FROM digest_feedback WHERE item_id=?`,
		`DELETE FROM digest_checkpoints WHERE job_id IN (SELECT id FROM jobs WHERE item_id=? AND stage='digest')`,
		`DELETE FROM job_failures WHERE job_id IN (SELECT id FROM jobs WHERE item_id=? AND stage='digest')`,
		`UPDATE filter_decisions SET override_of=NULL WHERE override_of IN (SELECT id FROM filter_decisions WHERE item_id=? AND stage='source')`,
		`DELETE FROM filter_decisions WHERE item_id=? AND stage='source'`,
		`DELETE FROM completeness_confirmations WHERE item_id=?`,
		`DELETE FROM digests WHERE item_id=?`,
		`DELETE FROM jobs WHERE item_id=? AND stage='digest'`,
	} {
		if _, err := tx.Exec(q, itemID); err != nil {
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	return paths, nil
}

// eraseDigest deletes the item's current digest with what hangs on it (prediction,
// feedback, knowledge, the digest job and its checkpoints and failures), except the
// job keepJob that is producing the replacement.
func (s *Store) eraseDigest(tx *sql.Tx, itemID, keepJob int64) (map[string]bool, error) {
	jobs, err := queryIDs(tx, `SELECT id FROM jobs WHERE item_id=? AND stage='digest' AND id<>?`, itemID, keepJob)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, q := range []string{
		`SELECT markdown_path FROM digests WHERE item_id=?`,
		`SELECT markdown_path FROM article_knowledge WHERE item_id=?`,
		`SELECT markdown_path FROM digest_checkpoints WHERE job_id IN ` + idList(jobs),
		`SELECT response_path FROM job_failures WHERE job_id IN ` + idList(jobs),
		`SELECT response_path FROM knowledge_failures WHERE run_id IN (SELECT id FROM knowledge_runs WHERE item_id=?)`,
	} {
		var rows *sql.Rows
		if strings.Contains(q, "?") {
			rows, err = tx.Query(q, itemID)
		} else {
			rows, err = tx.Query(q)
		}
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p string
			if err = rows.Scan(&p); err != nil {
				rows.Close()
				return nil, err
			}
			if p != "" {
				paths[p] = true
			}
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	for _, d := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM knowledge_failures WHERE run_id IN (SELECT id FROM knowledge_runs WHERE item_id=?)`, []any{itemID}},
		{`DELETE FROM article_knowledge WHERE item_id=?`, []any{itemID}},
		{`DELETE FROM knowledge_runs WHERE item_id=?`, []any{itemID}},
		{`DELETE FROM interest_predictions WHERE item_id=?`, []any{itemID}},
		{`DELETE FROM digest_feedback WHERE item_id=?`, []any{itemID}},
		{`DELETE FROM digest_checkpoints WHERE job_id IN ` + idList(jobs), nil},
		{`DELETE FROM job_failures WHERE job_id IN ` + idList(jobs), nil},
		{`DELETE FROM digests WHERE item_id=?`, []any{itemID}},
		{`DELETE FROM jobs WHERE id IN ` + idList(jobs), nil},
	} {
		if _, err = tx.Exec(d.query, d.args...); err != nil {
			return nil, fmt.Errorf("%s: %w", d.query, err)
		}
	}
	return paths, nil
}

func queryIDs(tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// idList renders integer IDs as an SQL list; IDs are never user text.
func idList(ids []int64) string {
	if len(ids) == 0 {
		return "(NULL)"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return "(" + strings.Join(parts, ",") + ")"
}
