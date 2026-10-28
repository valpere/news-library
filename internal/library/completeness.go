package library

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// snapshotSelect reads a source version with the user's latest completeness
// confirmation overlaid. The extractor's own value and warning stay untouched in
// source_versions; the overlay exists only while the latest event says complete.
const snapshotSelect = `SELECT v.id,v.item_id,v.text_path,v.title,v.hash,v.retrieved_at,v.origin,v.extractor,
 CASE WHEN c.complete=1 THEN 'full' ELSE v.completeness END,
 CASE WHEN c.complete=1 THEN 'Повноту підтвердив користувач ' || substr(c.recorded_at,1,10) || '; витяг: ' || v.warning ELSE v.warning END,
 coalesce(c.complete,0), CASE WHEN c.complete=1 THEN c.recorded_at ELSE '' END
 FROM source_versions v LEFT JOIN completeness_confirmations c ON c.id=(SELECT max(id) FROM completeness_confirmations WHERE snapshot_id=v.id)`

func scanSnapshot(row interface{ Scan(...any) error }) (Snapshot, string, error) {
	var v Snapshot
	var path string
	var confirmed int
	err := row.Scan(&v.ID, &v.ItemID, &path, &v.Title, &v.Hash, &v.RetrievedAt, &v.Origin, &v.Extractor, &v.Completeness, &v.Warning, &confirmed, &v.ConfirmedAt)
	v.UserConfirmed = confirmed == 1
	return v, path, err
}

// ConfirmCompleteness records the user's verdict that a saved text is (or, when
// complete is false, is no longer vouched to be) the whole original. Events are
// append-only and the latest one wins; the text itself never changes.
func (s *Store) ConfirmCompleteness(snapshotID int64, complete bool, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return errors.New("пояснення завелике")
	}
	flag := 0
	if complete {
		flag = 1
	}
	res, err := s.DB.Exec(`INSERT INTO completeness_confirmations(snapshot_id,complete,note,recorded_at)
 SELECT v.id,?,?,? FROM source_versions v WHERE v.id=? AND v.item_id IN (SELECT id FROM active_items)`, flag, note, nowUTC(), snapshotID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (w *Web) confirmCompleteness(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	source, e2 := strconv.ParseInt(r.PostForm.Get("source"), 10, 64)
	action := r.PostForm.Get("action")
	if err != nil || e2 != nil || (action != "confirm" && action != "withdraw") {
		http.Error(rw, "некоректний запит", http.StatusBadRequest)
		return
	}
	snap, err := w.Store.Snapshot(source)
	if err != nil || snap.ItemID != id {
		http.Error(rw, "текст не належить цьому матеріалу", http.StatusBadRequest)
		return
	}
	if err = w.Store.ConfirmCompleteness(source, action == "confirm", r.PostForm.Get("note")); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	} else if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, "/items/"+strconv.FormatInt(id, 10)+"?source="+strconv.FormatInt(source, 10), http.StatusSeeOther)
}
