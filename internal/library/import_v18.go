package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// One-off importer of a schema-18 database (versions and history) into the
// current model. Remove it, together with cmd/import-v18, once the live data
// has been moved (see docs/REDESIGN_DECISIONS.md, decision 18).

const importV18Schema = 18

// ImportReport counts what the import carried over.
type ImportReport struct {
	Items, Texts, Digests, Ratings, Trashed int
	WithoutDigest, QueuedForFetch           int
	SkippedLegacyRatings                    int
}

// ImportV18 copies the user's data and the current text and digest of every
// material from oldRoot (a data directory with a schema-18 library.sqlite) into
// newRoot, which must not exist or be empty. The source is only read. The result
// is built beside newRoot and moved into place on success, so a failure leaves no
// partial data. Knowledge, embeddings and connection notes are not carried over;
// the queue recalculates them.
func ImportV18(oldRoot, newRoot string) (ImportReport, error) {
	var report ImportReport
	oldRoot, err := filepath.Abs(oldRoot)
	if err != nil {
		return report, err
	}
	newRoot, err = filepath.Abs(newRoot)
	if err != nil {
		return report, err
	}
	if oldRoot == newRoot {
		return report, errors.New("source and target are the same directory")
	}
	if entries, err := os.ReadDir(newRoot); err == nil && len(entries) > 0 {
		return report, errors.New("target directory is not empty")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	old, err := openV18(oldRoot)
	if err != nil {
		return report, err
	}
	defer old.Close()
	work := newRoot + ".importing"
	if _, err = os.Lstat(work); err == nil {
		return report, fmt.Errorf("%s already exists; remove it first", work)
	}
	if err = os.MkdirAll(work, 0o700); err != nil {
		return report, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(work)
		}
	}()
	store, err := Open(filepath.Join(work, "library.sqlite"))
	if err != nil {
		return report, err
	}
	tx, err := store.DB.Begin()
	if err == nil {
		report, err = importV18Tx(tx, old, Files{Root: oldRoot}, store.Files)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ImportReport{}, err
	}
	// An empty target directory is replaced by the finished one.
	if err = os.Remove(newRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ImportReport{}, err
	}
	if err = os.Rename(work, newRoot); err != nil {
		keep = true // the finished import is the only copy: do not delete it
		return ImportReport{}, fmt.Errorf("import finished in %s but could not be moved: %w", work, err)
	}
	return report, nil
}

func openV18(root string) (*sql.DB, error) {
	path := filepath.Join(root, "library.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version != importV18Schema {
		db.Close()
		return nil, fmt.Errorf("source schema is %d; only %d can be imported", version, importV18Schema)
	}
	return db, nil
}

type v18Item struct {
	id                                               int64
	originalURL, canonicalURL, title, addedAt, state string
	errText, filterState                             string
	isRead                                           int
	snapshot                                         sql.NullInt64
}

func importV18Tx(tx *sql.Tx, old *sql.DB, from, to Files) (ImportReport, error) {
	var r ImportReport
	rows, err := old.Query(`SELECT id,original_url,canonical_url,title,added_at,is_read,state,error,filter_state,
 coalesce((SELECT v.id FROM source_versions v WHERE v.id=i.selected_snapshot AND v.item_id=i.id),(SELECT max(id) FROM source_versions WHERE item_id=i.id))
 FROM items i ORDER BY id`)
	if err != nil {
		return r, err
	}
	var items []v18Item
	for rows.Next() {
		var it v18Item
		if err = rows.Scan(&it.id, &it.originalURL, &it.canonicalURL, &it.title, &it.addedAt, &it.isRead, &it.state, &it.errText, &it.filterState, &it.snapshot); err != nil {
			rows.Close()
			return r, err
		}
		items = append(items, it)
	}
	if err = rows.Close(); err != nil {
		return r, err
	}
	for _, it := range items {
		if err = importV18Item(tx, old, from, to, it, &r); err != nil {
			return r, fmt.Errorf("item %d: %w", it.id, err)
		}
	}
	for _, c := range [][2]string{
		{"source_aliases", "url,item_id"},
		{"item_tags", "item_id,tag,origin"},
		{"trash_entries", "item_id,reason,moved_at"},
		{"search_events", "id,query,mode,actual_mode,filters,result_ids,created_at,not_found_at"},
		{"search_judgments", "id,event_id,item_id,score,recorded_at"},
	} {
		if err = copyV18(tx, old, c[0], c[1]); err != nil {
			return r, err
		}
	}
	if err = old.QueryRow(`SELECT count(*) FROM rating_events WHERE scale<>'estimative_usefulness_v1'`).Scan(&r.SkippedLegacyRatings); err != nil {
		return r, err
	}
	if err = tx.QueryRow(`SELECT count(*) FROM trash_entries`).Scan(&r.Trashed); err != nil {
		return r, err
	}
	// Trashed items are not searchable (as MarkJunk leaves them).
	_, err = tx.Exec(`DELETE FROM item_fts WHERE rowid IN (SELECT item_id FROM trash_entries)`)
	return r, err
}

// copyV18 copies the listed columns of a table of the same name, row by row.
func copyV18(tx *sql.Tx, old *sql.DB, table, columns string) error {
	n := strings.Count(columns, ",") + 1
	insert := "INSERT INTO " + table + "(" + columns + ") VALUES(?" + strings.Repeat(",?", n-1) + ")"
	rows, err := old.Query("SELECT " + columns + " FROM " + table + " ORDER BY 1")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, n)
		pointers := make([]any, n)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return err
		}
		if _, err = tx.Exec(insert, values...); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	return rows.Err()
}

func importV18Item(tx *sql.Tx, old *sql.DB, from, to Files, it v18Item, r *ImportReport) error {
	r.Items++
	reason, err := v18FilterReason(old, it.id)
	if err != nil {
		return err
	}
	var rating sql.NullInt64
	var ratedAt sql.NullString
	err = old.QueryRow(`SELECT score,coalesce(assessed_at,recorded_at) FROM rating_events WHERE item_id=? AND scale='estimative_usefulness_v1' ORDER BY id DESC LIMIT 1`, it.id).Scan(&rating, &ratedAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if rating.Valid {
		r.Ratings++
	}
	// Only an unfinished fetch of a live item is queued again; terminal states
	// (needs_input, failed, ...) and trashed items stay as they were.
	state := it.state
	var trashed int
	if err = old.QueryRow(`SELECT count(*) FROM trash_entries WHERE item_id=?`, it.id).Scan(&trashed); err != nil {
		return err
	}
	refetch := !it.snapshot.Valid && trashed == 0 && (state == "queued" || state == "running" || state == "enriching")
	if refetch {
		state = "queued"
	}
	if _, err = tx.Exec(`INSERT INTO items(id,original_url,canonical_url,title,added_at,is_read,state,error,rating,rated_at,filter_state,filter_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.id, it.originalURL, it.canonicalURL, it.title, it.addedAt, it.isRead, state, it.errText, rating, ratedAt, it.filterState, reason); err != nil {
		return err
	}
	title, text := "", ""
	if it.snapshot.Valid {
		if title, text, err = importV18Text(tx, old, from, to, it.id, it.snapshot.Int64); err != nil {
			return err
		}
		r.Texts++
		digest, e := importV18Digest(tx, old, from, to, it.id, it.snapshot.Int64)
		if e != nil {
			return e
		}
		if digest {
			r.Digests++
		} else {
			r.WithoutDigest++
		}
	} else if refetch {
		if _, err = tx.Exec(`INSERT INTO jobs(item_id) VALUES(?)`, it.id); err != nil {
			return err
		}
		r.QueuedForFetch++
	}
	if _, err = tx.Exec(`INSERT INTO item_fts(rowid,title,text,url) VALUES(?,?,?,?)`, it.id, title, text, it.canonicalURL); err != nil {
		return err
	}
	return nil
}

func v18FilterReason(old *sql.DB, item int64) (string, error) {
	var probability, threshold float64
	var contributions string
	err := old.QueryRow(`SELECT probability,threshold,contributions FROM filter_decisions WHERE item_id=? ORDER BY id DESC LIMIT 1`, item).Scan(&probability, &threshold, &contributions)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var list []Contribution
	if err = json.Unmarshal([]byte(contributions), &list); err != nil {
		return "", err
	}
	return filterReason(probability, threshold, list), nil
}

func importV18Text(tx *sql.Tx, old *sql.DB, from, to Files, item, snapshot int64) (title, text string, err error) {
	var hash, path, retrieved, origin, extractor, completeness, warning string
	if err = old.QueryRow(`SELECT title,hash,text_path,retrieved_at,origin,extractor,completeness,warning FROM source_versions WHERE id=?`, snapshot).
		Scan(&title, &hash, &path, &retrieved, &origin, &extractor, &completeness, &warning); err != nil {
		return
	}
	data, err := from.Read(path, hash)
	if err != nil {
		return "", "", err
	}
	newPath, err := to.Put("texts", data)
	if err != nil {
		return "", "", err
	}
	var confirmed int
	err = old.QueryRow(`SELECT complete FROM completeness_confirmations WHERE snapshot_id=? ORDER BY id DESC LIMIT 1`, snapshot).Scan(&confirmed)
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	_, err = tx.Exec(`INSERT INTO texts(item_id,title,hash,retrieved_at,origin,extractor,completeness,warning,text_path,complete_confirmed) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		item, title, hash, retrieved, origin, extractor, completeness, warning, newPath, confirmed)
	return title, string(data), err
}

// importV18Digest carries the newest digest of the current text, if any.
func importV18Digest(tx *sql.Tx, old *sql.DB, from, to Files, item, snapshot int64) (bool, error) {
	var id, in, out, chunks int64
	var model, actual, provider, prompt, path, hash string
	err := old.QueryRow(`SELECT id,model,actual_model,provider,prompt_version,input_tokens,output_tokens,markdown_path,hash,chunk_count FROM generated_artifacts WHERE item_id=? AND snapshot_id=? ORDER BY id DESC LIMIT 1`, item, snapshot).
		Scan(&id, &model, &actual, &provider, &prompt, &in, &out, &path, &hash, &chunks)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	data, err := from.Read(path, hash)
	if err != nil {
		return false, err
	}
	newPath, err := to.Put("digests", data)
	if err != nil {
		return false, err
	}
	var feedback string
	err = old.QueryRow(`SELECT quality FROM digest_feedback WHERE digest_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&feedback)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if _, err = tx.Exec(`INSERT INTO digests(item_id,model,actual_model,provider,prompt_version,input_tokens,output_tokens,markdown_path,hash,chunk_count,feedback) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		item, model, actual, provider, prompt, in, out, newPath, hash, max(chunks, 1), feedback); err != nil {
		return false, err
	}
	var p struct {
		probability                  float64
		reason, profile, profileHash string
	}
	err = old.QueryRow(`SELECT probability,reason,profile,profile_hash FROM interest_predictions WHERE digest_id=?`, id).Scan(&p.probability, &p.reason, &p.profile, &p.profileHash)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO interest_predictions(item_id,probability,reason,profile,profile_hash) VALUES(?,?,?,?,?)`, item, p.probability, p.reason, p.profile, p.profileHash)
	return true, err
}
