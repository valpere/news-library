package library

import (
	"database/sql"
	"fmt"
	"strings"
)

// replaceText is called once sid has become the item's current text, replacing an
// earlier one: the human rating no longer describes this text, so it is erased,
// and so is everything stored for the earlier texts.
func (s *Store) replaceText(tx *sql.Tx, itemID, sid int64) (map[string]bool, error) {
	if _, err := tx.Exec(`UPDATE items SET rating=NULL,rated_at=NULL WHERE id=?`, itemID); err != nil {
		return nil, err
	}
	return s.eraseEarlierTexts(tx, itemID, sid)
}

// eraseEarlierTexts removes everything stored for the item's texts other than the
// current one (keep): the earlier text rows, their digests, checkpoints, knowledge,
// embeddings, filter decisions, completeness confirmations and the jobs that made
// them. A text a connection note mentions stays until notes lose their revisions.
// It returns the file paths the erased rows used; the caller removes the ones
// nothing else uses after the transaction commits.
func (s *Store) eraseEarlierTexts(tx *sql.Tx, itemID, keep int64) (map[string]bool, error) {
	snaps, err := queryIDs(tx, `SELECT id FROM source_versions WHERE item_id=? AND id<>? AND id NOT IN (SELECT snapshot_id FROM connection_members)`, itemID, keep)
	if err != nil || len(snaps) == 0 {
		return nil, err
	}
	in := idList(snaps)
	arts, err := queryIDs(tx, `SELECT id FROM generated_artifacts WHERE snapshot_id IN `+in)
	if err != nil {
		return nil, err
	}
	runs, err := queryIDs(tx, `SELECT id FROM knowledge_runs WHERE snapshot_id IN `+in)
	if err != nil {
		return nil, err
	}
	jobs, err := queryIDs(tx, `SELECT id FROM jobs WHERE snapshot_id IN `+in+` OR id IN (SELECT job_id FROM source_versions WHERE id IN `+in+`) OR id IN (SELECT job_id FROM generated_artifacts WHERE snapshot_id IN `+in+`)`)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, q := range []string{
		`SELECT text_path FROM source_versions WHERE id IN ` + in,
		`SELECT markdown_path FROM generated_artifacts WHERE snapshot_id IN ` + in,
		`SELECT markdown_path FROM digest_checkpoints WHERE job_id IN ` + idList(jobs),
		`SELECT markdown_path FROM article_knowledge WHERE snapshot_id IN ` + in,
		`SELECT response_path FROM job_failures WHERE job_id IN ` + idList(jobs),
		`SELECT response_path FROM knowledge_failures WHERE run_id IN ` + idList(runs),
	} {
		rows, e := tx.Query(q)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				rows.Close()
				return nil, e
			}
			if p != "" {
				paths[p] = true
			}
		}
		if e = rows.Close(); e != nil {
			return nil, e
		}
	}
	for _, q := range []string{
		`DELETE FROM embedding_chunks WHERE snapshot_id IN ` + in,
		`DELETE FROM embedding_runs WHERE snapshot_id IN ` + in,
		`DELETE FROM knowledge_failures WHERE run_id IN ` + idList(runs),
		`DELETE FROM article_knowledge WHERE snapshot_id IN ` + in,
		`DELETE FROM knowledge_runs WHERE snapshot_id IN ` + in,
		`DELETE FROM interest_predictions WHERE digest_id IN ` + idList(arts),
		`DELETE FROM digest_feedback WHERE digest_id IN ` + idList(arts),
		`DELETE FROM digest_checkpoints WHERE job_id IN ` + idList(jobs),
		`DELETE FROM job_failures WHERE job_id IN ` + idList(jobs),
		`UPDATE filter_decisions SET override_of=NULL WHERE override_of IN (SELECT id FROM filter_decisions WHERE snapshot_id IN ` + in + `)`,
		`DELETE FROM filter_decisions WHERE snapshot_id IN ` + in,
		`DELETE FROM completeness_confirmations WHERE snapshot_id IN ` + in,
		`DELETE FROM generated_artifacts WHERE snapshot_id IN ` + in,
		`UPDATE source_versions SET job_id=NULL WHERE id IN ` + in,
		`DELETE FROM jobs WHERE id IN ` + idList(jobs),
		`DELETE FROM source_versions WHERE id IN ` + in,
	} {
		if _, err = tx.Exec(q); err != nil {
			return nil, fmt.Errorf("%s: %w", q, err)
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

// eraseEarlierDigests removes the other digests of the same text (keep is the new
// one) with their predictions, feedback, knowledge, checkpoints, failures and
// jobs. A digest a connection note mentions stays until notes lose revisions.
func (s *Store) eraseEarlierDigests(tx *sql.Tx, snapshotID, keep int64) (map[string]bool, error) {
	arts, err := queryIDs(tx, `SELECT id FROM generated_artifacts WHERE snapshot_id=? AND id<>? AND id NOT IN (SELECT digest_id FROM connection_members WHERE digest_id IS NOT NULL)`, snapshotID, keep)
	if err != nil || len(arts) == 0 {
		return nil, err
	}
	in := idList(arts)
	runs, err := queryIDs(tx, `SELECT id FROM knowledge_runs WHERE digest_id IN `+in)
	if err != nil {
		return nil, err
	}
	jobs, err := queryIDs(tx, `SELECT job_id FROM generated_artifacts WHERE id IN `+in)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, q := range []string{
		`SELECT markdown_path FROM generated_artifacts WHERE id IN ` + in,
		`SELECT markdown_path FROM digest_checkpoints WHERE job_id IN ` + idList(jobs),
		`SELECT markdown_path FROM article_knowledge WHERE run_id IN ` + idList(runs),
		`SELECT response_path FROM job_failures WHERE job_id IN ` + idList(jobs),
		`SELECT response_path FROM knowledge_failures WHERE run_id IN ` + idList(runs),
	} {
		rows, e := tx.Query(q)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				rows.Close()
				return nil, e
			}
			if p != "" {
				paths[p] = true
			}
		}
		if e = rows.Close(); e != nil {
			return nil, e
		}
	}
	for _, q := range []string{
		`DELETE FROM knowledge_failures WHERE run_id IN ` + idList(runs),
		`DELETE FROM article_knowledge WHERE run_id IN ` + idList(runs),
		`DELETE FROM knowledge_runs WHERE id IN ` + idList(runs),
		`DELETE FROM interest_predictions WHERE digest_id IN ` + in,
		`DELETE FROM digest_feedback WHERE digest_id IN ` + in,
		`DELETE FROM digest_checkpoints WHERE job_id IN ` + idList(jobs),
		`DELETE FROM job_failures WHERE job_id IN ` + idList(jobs),
		`DELETE FROM generated_artifacts WHERE id IN ` + in,
		`DELETE FROM jobs WHERE id IN ` + idList(jobs),
	} {
		if _, err = tx.Exec(q); err != nil {
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	return paths, nil
}
