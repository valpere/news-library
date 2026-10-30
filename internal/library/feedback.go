package library

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

type DigestFeedback struct {
	ID, ItemID, DigestID      int64
	Quality, Note, RecordedAt string
}

func (s *Store) Feedback(itemID, digestID int64, quality, note string) error {
	if quality != "good" && quality != "inaccurate" && quality != "incomplete" && quality != "unclear" {
		return errors.New("оберіть якість дайджесту")
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 4000 {
		return errors.New("відгук завеликий")
	}
	if digestID != itemID {
		return errors.New("дайджест не належить цьому матеріалу")
	}
	r, err := s.DB.Exec("INSERT INTO digest_feedback(item_id,quality,note,recorded_at) SELECT item_id,?,?,? FROM digests WHERE item_id=?", quality, note, nowUTC(), itemID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return errors.New("дайджест не знайдено")
	}
	return nil
}
func (s *Store) FeedbackHistory(itemID, digestID int64) ([]DigestFeedback, error) {
	rows, err := s.DB.Query("SELECT id,item_id,item_id,quality,note,recorded_at FROM digest_feedback WHERE item_id=? AND item_id=? ORDER BY id DESC", itemID, digestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DigestFeedback{}
	for rows.Next() {
		var f DigestFeedback
		if err = rows.Scan(&f.ID, &f.ItemID, &f.DigestID, &f.Quality, &f.Note, &f.RecordedAt); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

// Regeneration is an explicit action with a new job; the new digest replaces the
// current one (and its feedback) when it completes.
func (s *Store) RegenerateDigest(itemID int64) error {
	if s.DigestConfig == nil {
		return errors.New("генерацію не налаштовано")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var selected sql.NullInt64
	var filtered string
	if err = tx.QueryRow("SELECT (SELECT item_id FROM texts WHERE texts.item_id=items.id),filter_state FROM items WHERE id=?", itemID).Scan(&selected, &filtered); err != nil {
		return err
	}
	if !selected.Valid {
		return errors.New("спершу потрібен текст матеріалу")
	}
	snapshotID := selected.Int64
	if filtered == "rejected" {
		return errors.New("спершу явно поверніть матеріал до обробки")
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND stage='digest' AND state IN ('queued','running','retry_wait')", snapshotID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("генерація вже активна")
	}
	if rejected, e := s.ensureFilterForSnapshot(tx, itemID, snapshotID); e != nil {
		return e
	} else if rejected {
		return tx.Commit()
	}
	if err = s.queueRegeneration(tx, itemID, snapshotID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) queueRegeneration(tx *sql.Tx, itemID, snapshotID int64) error {
	var err error
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND stage='digest' AND state IN ('queued','running','retry_wait')", snapshotID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("генерація вже активна")
	}
	c := s.DigestConfig
	_, err = tx.Exec("INSERT INTO jobs(item_id,stage,config_version,model,prompt_version,input_limit,interests,chunk_limit,chunk_max_parts) VALUES(?,'digest',?,?,?,?,?,?,?)", itemID, c.Version()+"|regenerate:"+rand.Text(), c.Model, c.PromptVersion, c.MaxInputBytes, c.Interests, c.ChunkLimit, c.ChunkMaxParts)
	if err != nil {
		return err
	}
	return nil
}
