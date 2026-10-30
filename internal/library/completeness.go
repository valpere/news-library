package library

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

// snapshotSelect reads a source with the user's completeness confirmation overlaid.
// The extractor's own value and warning stay untouched in texts; the overlay exists
// only while the confirmation flag is set.
const snapshotSelect = `SELECT v.item_id,v.item_id,v.text_path,v.title,v.hash,v.retrieved_at,v.origin,v.extractor,
 CASE WHEN v.complete_confirmed=1 THEN 'full' ELSE v.completeness END,
 CASE WHEN v.complete_confirmed=1 THEN 'Повноту підтвердив користувач; витяг: ' || v.warning ELSE v.warning END,
 v.complete_confirmed
 FROM texts v`

func scanSnapshot(row interface{ Scan(...any) error }) (Snapshot, string, error) {
	var v Snapshot
	var path string
	var confirmed int
	err := row.Scan(&v.ID, &v.ItemID, &path, &v.Title, &v.Hash, &v.RetrievedAt, &v.Origin, &v.Extractor, &v.Completeness, &v.Warning, &confirmed)
	v.UserConfirmed = confirmed == 1
	return v, path, err
}

// ConfirmCompleteness sets the user's verdict that the saved text is (or, when
// complete is false, is no longer vouched to be) the whole original. The text itself
// never changes; replacing the text clears the flag.
func (s *Store) ConfirmCompleteness(itemID int64, complete bool) error {
	flag := 0
	if complete {
		flag = 1
	}
	res, err := s.DB.Exec(`UPDATE texts SET complete_confirmed=? WHERE item_id=? AND item_id IN (SELECT id FROM active_items)`, flag, itemID)
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
	action := r.PostForm.Get("action")
	if err != nil || (action != "confirm" && action != "withdraw") {
		http.Error(rw, "некоректний запит", http.StatusBadRequest)
		return
	}
	if err = w.Store.ConfirmCompleteness(id, action == "confirm"); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	} else if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, "/items/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
