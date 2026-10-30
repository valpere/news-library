package library

import (
	"crypto/rand"
	"database/sql"
	"errors"
)

// Feedback sets (or, with "", clears) the digest's quality flag (one current value; a new digest starts without it).
func (s *Store) Feedback(itemID int64, quality string) error {
	// An empty quality clears the flag.
	if quality != "" && quality != "good" && quality != "inaccurate" && quality != "incomplete" && quality != "unclear" {
		return errors.New("оберіть якість дайджесту")
	}
	r, err := s.DB.Exec("UPDATE digests SET feedback=? WHERE item_id=?", quality, itemID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return errors.New("дайджест не знайдено")
	}
	return nil
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
	if rejected, e := s.ensureFilter(tx, itemID, snapshotID); e != nil {
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
