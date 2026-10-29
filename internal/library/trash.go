package library

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Keep the identity and immutable history, but remove the URL from the active collection.
func trashMissingPage(tx *sql.Tx, id int64, reason string) error {
	_, err := tx.Exec(`INSERT INTO trash_entries(item_id,reason,moved_at)
 SELECT id,?,? FROM items WHERE id=?
 AND NOT EXISTS(SELECT 1 FROM source_versions WHERE item_id=items.id)
 AND NOT EXISTS(SELECT 1 FROM rating_events WHERE item_id=items.id)
 AND NOT EXISTS(SELECT 1 FROM jobs WHERE item_id=items.id AND state IN ('queued','running','retry_wait'))
 ON CONFLICT(item_id) DO NOTHING`, reason, nowUTC(), id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM item_fts WHERE rowid=? AND EXISTS(SELECT 1 FROM trash_entries WHERE item_id=?)`, id, id)
	return err
}

// MarkJunk is the user's own verdict that a material is junk: it records a
// zero usefulness rating (with the current source and digest as context) and
// moves the item to the trash in one transaction. Unlike an unavailable page,
// its texts, digests and rating history are kept. Repeating it changes nothing.
func (s *Store) MarkJunk(id int64, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 500 {
		return errors.New("пояснення завелике")
	}
	reason := "сміття, позначено користувачем"
	if note != "" {
		reason += ": " + note
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRow(`SELECT count(*) FROM active_items WHERE id=?`, id).Scan(&active); err != nil {
		return err
	}
	if active == 0 {
		var trashed int
		if err = tx.QueryRow(`SELECT count(*) FROM trash_entries WHERE item_id=?`, id).Scan(&trashed); err != nil {
			return err
		}
		if trashed == 1 {
			return nil
		}
		return sql.ErrNoRows
	}
	if _, err = tx.Exec(`INSERT INTO rating_events(item_id,score,recorded_at,source_version_id,digest_id,reason,scale)
 SELECT i.id,0,?,i.selected_snapshot,(SELECT max(g.id) FROM generated_artifacts g WHERE g.snapshot_id=i.selected_snapshot),?,? FROM active_items i WHERE i.id=?`,
		nowUTC(), reason, UsefulnessScale, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO trash_entries(item_id,reason,moved_at) VALUES(?,?,?)`, id, reason, nowUTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM item_fts WHERE rowid=?`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE jobs SET state='cancelled' WHERE item_id=? AND state IN ('queued','retry_wait')`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Web) markJunk(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if r.PostForm.Get("confirm") != "1" {
		http.Error(rw, "підтвердіть перенесення в помийку", http.StatusBadRequest)
		return
	}
	if err = w.Store.MarkJunk(id, r.PostForm.Get("note")); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	} else if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, "/trash", http.StatusSeeOther)
}

func (s *Store) TrashReason(id int64) (string, error) {
	var reason string
	err := s.DB.QueryRow("SELECT reason FROM trash_entries WHERE item_id=?", id).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return reason, err
}

type TrashEntry struct {
	ID                   int64
	URL, Reason, MovedAt string
}

func (s *Store) Trash() ([]TrashEntry, error) {
	rows, err := s.DB.Query(`SELECT t.item_id,i.original_url,t.reason,t.moved_at FROM trash_entries t JOIN items i ON i.id=t.item_id ORDER BY t.moved_at DESC,t.item_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []TrashEntry{}
	for rows.Next() {
		var e TrashEntry
		if err = rows.Scan(&e.ID, &e.URL, &e.Reason, &e.MovedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// TrashCard is what the trash shows for one material: where it came from, why it
// was moved and the text kept, so the user can decide to restore it.
type TrashCard struct {
	ID                                int64
	URL, Title, Reason, MovedAt, Text string
	HasText                           bool
}

func (s *Store) TrashCard(id int64) (TrashCard, error) {
	var c TrashCard
	var selected int64
	err := s.DB.QueryRow(`SELECT i.id,i.original_url,i.title,t.reason,t.moved_at,coalesce(i.selected_snapshot,0)
 FROM items i JOIN trash_entries t ON t.item_id=i.id WHERE i.id=?`, id).Scan(&c.ID, &c.URL, &c.Title, &c.Reason, &c.MovedAt, &selected)
	if err != nil {
		return c, err
	}
	if selected > 0 {
		snap, e := s.Snapshot(selected)
		if e != nil {
			return c, e
		}
		c.Text, c.HasText = snap.Text, true
		if c.Title == "" {
			c.Title = snap.Title
		}
	}
	return c, nil
}

// RestoreFromTrash brings a material back to the active collection. Only the
// trash entry goes away: texts, digests and the rating history, including the
// zero rating a junk verdict recorded, stay as they were. The search index entry
// is rebuilt from the selected text.
func (s *Store) RestoreFromTrash(id int64) error {
	card, err := s.TrashCard(id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM trash_entries WHERE item_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if _, err = tx.Exec(`INSERT INTO item_fts(rowid,title,text,url) VALUES(?,?,?,(SELECT canonical_url FROM items WHERE id=?))`, id, card.Title, card.Text, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Web) restoreFromTrash(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if err = w.Store.RestoreFromTrash(id); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	} else if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(rw, r, "/items/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (w *Web) trashPage(rw http.ResponseWriter, r *http.Request) {
	entries, err := w.Store.Trash()
	if err != nil {
		http.Error(rw, "Не вдалося прочитати помийку", 500)
		return
	}
	w.render(rw, "trash", pageData{TrashEntries: entries})
}
