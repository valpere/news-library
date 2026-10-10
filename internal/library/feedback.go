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
	_, err := s.DB.Exec("INSERT INTO digest_feedback(item_id,digest_id,quality,note,recorded_at) VALUES(?,?,?,?,?)", itemID, digestID, quality, note, nowUTC())
	return err
}
func (s *Store) FeedbackHistory(itemID, digestID int64) ([]DigestFeedback, error) {
	rows, err := s.DB.Query("SELECT id,item_id,digest_id,quality,note,recorded_at FROM digest_feedback WHERE item_id=? AND digest_id=? ORDER BY id DESC", itemID, digestID)
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
func (s *Store) DigestsForSnapshot(sid int64) ([]Digest, error) {
	rows, err := s.DB.Query("SELECT id,item_id,snapshot_id,job_id,model,actual_model,provider,prompt_version,created_at FROM generated_artifacts WHERE snapshot_id=? ORDER BY id DESC", sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Digest{}
	for rows.Next() {
		var d Digest
		if err = rows.Scan(&d.ID, &d.ItemID, &d.SnapshotID, &d.JobID, &d.Model, &d.ActualModel, &d.Provider, &d.PromptVersion, &d.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// Regeneration is an explicit action with a new job/artifact; previous digests
// and their rating/feedback references remain intact.
func (s *Store) RegenerateDigest(itemID, snapshotID int64) error {
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
	if err = tx.QueryRow("SELECT selected_snapshot,filter_state FROM items WHERE id=?", itemID).Scan(&selected, &filtered); err != nil {
		return err
	}
	if !selected.Valid || selected.Int64 != snapshotID {
		return errors.New("повторна генерація доступна для поточної версії тексту")
	}
	if filtered == "rejected" {
		return errors.New("спершу явно поверніть матеріал до обробки")
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE snapshot_id=? AND stage='digest' AND state IN ('queued','running','retry_wait')", snapshotID).Scan(&active); err != nil {
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
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE snapshot_id=? AND stage='digest' AND state IN ('queued','running','retry_wait')", snapshotID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("генерація вже активна")
	}
	c := s.DigestConfig
	_, err = tx.Exec("INSERT INTO jobs(item_id,stage,snapshot_id,config_version,model,prompt_version,input_limit,interests,chunk_limit,chunk_max_parts) VALUES(?,'digest',?,?,?,?,?,?,?,?)", itemID, snapshotID, c.Version()+"|regenerate:"+rand.Text(), c.Model, c.PromptVersion, c.MaxInputBytes, c.Interests, c.ChunkLimit, c.ChunkMaxParts)
	if err != nil {
		return err
	}
	return nil
}
