package library

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type DigestConfig struct {
	Model, PromptVersion      string
	MaxInputBytes             int
	Interests                 string
	ChunkLimit, ChunkMaxParts int
}

func (c DigestConfig) Version() string {
	version := fmt.Sprintf("%s|%s|%d", c.Model, c.PromptVersion, c.MaxInputBytes)
	if c.Interests != "" {
		version += "|" + profileHash(c.Interests)
	}
	if c.ChunkLimit > 0 {
		version += fmt.Sprintf("|chunks-v1:%d:%d", c.ChunkLimit, c.ChunkMaxParts)
	}
	return version
}

type Digest struct {
	ID, ItemID, SnapshotID, JobID                                    int64
	Markdown, Model, ActualModel, Provider, PromptVersion, CreatedAt string
	InputTokens, OutputTokens                                        int64
	Prediction                                                       *InterestPrediction
	ChunkCount                                                       int
}

// EnableDigests schedules the first digest for existing unprocessed snapshots.
// Changing configuration leaves existing results and decisions alone.
func (s *Store) EnableDigests(c DigestConfig) error {
	c.Interests = strings.TrimSpace(c.Interests)
	if c.ChunkLimit != 0 && (c.ChunkLimit < 1024 || c.ChunkLimit > c.MaxInputBytes || c.ChunkMaxParts < 2 || c.ChunkMaxParts > 512) {
		return errors.New("invalid chunk configuration")
	}
	if len(c.Interests) > 8000 || (c.Interests != "" && c.PromptVersion != DigestPredictionPromptVersion) || (c.Interests == "" && c.PromptVersion == DigestPredictionPromptVersion) {
		return errors.New("invalid interest profile configuration")
	}

	if !cloudModelID(c.Model) || (c.PromptVersion != DigestPromptVersion && c.PromptVersion != DigestPredictionPromptVersion) || c.MaxInputBytes < 1024 {
		return errors.New("invalid digest configuration")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,selected_snapshot FROM items WHERE selected_snapshot IS NOT NULL AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE snapshot_id=items.selected_snapshot) AND NOT EXISTS(SELECT 1 FROM jobs WHERE stage='digest' AND snapshot_id=items.selected_snapshot)`)
	if err != nil {
		return err
	}
	var pairs [][2]int64
	for rows.Next() {
		var pair [2]int64
		if err = rows.Scan(&pair[0], &pair[1]); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, pair)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	old := s.DigestConfig
	s.DigestConfig = &c
	for _, pair := range pairs {
		if err = s.queueDigest(tx, pair[0], pair[1]); err != nil {
			s.DigestConfig = old
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		s.DigestConfig = old
	}
	return err
}
func (s *Store) queueDigest(tx *sql.Tx, itemID, snapshotID int64) error {
	if s.DigestConfig == nil {
		return nil
	}
	rejected, err := s.ensureFilterForSnapshot(tx, itemID, snapshotID)
	if err != nil || rejected {
		return err
	}
	c := s.DigestConfig
	_, err = tx.Exec(`INSERT INTO jobs(item_id,stage,snapshot_id,config_version,model,prompt_version,input_limit,interests,chunk_limit,chunk_max_parts) VALUES(?,'digest',?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, itemID, snapshotID, c.Version(), c.Model, c.PromptVersion, c.MaxInputBytes, c.Interests, c.ChunkLimit, c.ChunkMaxParts)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE items SET state='enriching',error='' WHERE id=? AND selected_snapshot=? AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE snapshot_id=?)`, itemID, snapshotID, snapshotID)
	return err
}
func (s *Store) Snapshot(id int64) (Snapshot, error) {
	var v Snapshot
	var path string
	err := s.DB.QueryRow(`SELECT id,item_id,text_path,title,hash,retrieved_at,origin,extractor,completeness,warning FROM source_versions WHERE id=?`, id).Scan(&v.ID, &v.ItemID, &path, &v.Title, &v.Hash, &v.RetrievedAt, &v.Origin, &v.Extractor, &v.Completeness, &v.Warning)
	if err != nil {
		return v, err
	}
	data, err := s.Files.Read(path, v.Hash)
	v.Text = string(data)
	return v, err
}
func (s *Store) CompleteDigest(j Job, d Digest) error {
	if j.Stage != "digest" || !j.SnapshotID.Valid {
		return errors.New("not a digest job")
	}
	if _, err := SplitDigest(d.Markdown); err != nil {
		return err
	}
	if j.Interests != "" {
		if err := validatePrediction(d.Prediction); err != nil {
			return err
		}
		if d.Prediction.Profile != j.Interests {
			return errors.New("prediction profile mismatch")
		}
	} else if d.Prediction != nil {
		return errors.New("prediction requires a configured profile")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseValid(tx, j); err != nil {
		return err
	}
	path, err := s.Files.Put("digests", []byte(d.Markdown))
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(d.Markdown)))
	result, err := tx.Exec(`INSERT INTO generated_artifacts(item_id,snapshot_id,job_id,markdown_path,model,actual_model,provider,prompt_version,created_at,input_tokens,output_tokens,hash,chunk_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, j.ItemID, j.SnapshotID.Int64, j.ID, path, j.Model, d.ActualModel, d.Provider, j.PromptVersion, nowUTC(), d.InputTokens, d.OutputTokens, hash, max(d.ChunkCount, 1))
	if err != nil {
		return err
	}
	if d.Prediction != nil {
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		p := d.Prediction
		if _, err = tx.Exec("INSERT INTO interest_predictions(digest_id,probability,reason,profile,profile_hash) VALUES(?,?,?,?,?)", id, p.Probability, p.Reason, j.Interests, profileHash(j.Interests)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE jobs SET state='done',lease_until=0,error='' WHERE id=?`, j.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE items SET state='ready',error='' WHERE id=? AND selected_snapshot=?`, j.ItemID, j.SnapshotID); err != nil {
		return err
	}
	return tx.Commit()
}

const digestSelect = `SELECT id,item_id,snapshot_id,job_id,markdown_path,model,actual_model,provider,prompt_version,created_at,input_tokens,output_tokens,hash,chunk_count FROM generated_artifacts`

func (s *Store) scanDigest(row interface{ Scan(...any) error }) (Digest, error) {
	var d Digest
	var path, hash string
	err := row.Scan(&d.ID, &d.ItemID, &d.SnapshotID, &d.JobID, &path, &d.Model, &d.ActualModel, &d.Provider, &d.PromptVersion, &d.CreatedAt, &d.InputTokens, &d.OutputTokens, &hash, &d.ChunkCount)
	if err != nil {
		return d, err
	}
	data, err := s.Files.Read(path, hash)
	d.Markdown = string(data)
	return d, err
}
func (s *Store) DigestForSnapshot(id int64) (*Digest, error) {
	d, e := s.scanDigest(s.DB.QueryRow(digestSelect+" WHERE snapshot_id=? ORDER BY id DESC LIMIT 1", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e == nil {
		d.Prediction, e = s.Prediction(d.ID)
	}
	return &d, e
}
func (s *Store) Digest(id, itemID int64) (Digest, error) {
	d, e := s.scanDigest(s.DB.QueryRow(digestSelect+" WHERE id=? AND item_id=?", id, itemID))
	if e == nil {
		d.Prediction, e = s.Prediction(d.ID)
	}
	return d, e
}
func (s *Store) RetryDigest(itemID int64) error {
	if s.DigestConfig == nil {
		return errors.New("генерацію не налаштовано")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sid sql.NullInt64
	if err = tx.QueryRow("SELECT selected_snapshot FROM items WHERE id=?", itemID).Scan(&sid); err != nil {
		return err
	}
	if !sid.Valid {
		return errors.New("спершу потрібен текст")
	}
	var filtered string
	if err = tx.QueryRow("SELECT filter_state FROM items WHERE id=?", itemID).Scan(&filtered); err != nil {
		return err
	}
	if filtered == "rejected" {
		return errors.New("спершу явно поверніть матеріал до обробки")
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE stage='digest' AND snapshot_id=? AND state IN ('queued','running','retry_wait')", sid).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("генерація вже активна")
	}
	c := s.DigestConfig
	var state string
	err = tx.QueryRow("SELECT state FROM jobs WHERE stage='digest' AND snapshot_id=? AND config_version=?", sid, c.Version()).Scan(&state)
	if err == nil {
		if state != "failed" && state != "cancelled" {
			return errors.New("дайджест уже готовий або генерація активна")
		}
		if _, err = tx.Exec("UPDATE jobs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='' WHERE stage='digest' AND snapshot_id=? AND config_version=?", sid, c.Version()); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = s.queueDigest(tx, itemID, sid.Int64); err != nil {
		return err
	}
	return tx.Commit()
}

// A migrated source may not have a rule decision yet. Evaluate it before its
// first new generation; configuration changes leave existing decisions alone.
func (s *Store) ensureFilterForSnapshot(tx *sql.Tx, itemID, snapshotID int64) (bool, error) {
	var decisions int
	if err := tx.QueryRow("SELECT count(*) FROM filter_decisions WHERE item_id=? AND snapshot_id=?", itemID, snapshotID).Scan(&decisions); err != nil {
		return false, err
	}
	if decisions == 0 {
		var snap Snapshot
		var path, hash string
		if err := tx.QueryRow("SELECT text_path,hash,title FROM source_versions WHERE id=? AND item_id=?", snapshotID, itemID).Scan(&path, &hash, &snap.Title); err != nil {
			return false, err
		}
		data, err := s.Files.Read(path, hash)
		if err != nil {
			return false, err
		}
		snap.Text = string(data)
		snap.ID = snapshotID
		if rejected, err := s.decideFilter(tx, itemID, &snap, false); err != nil || rejected {
			return rejected, err
		}
	}
	var filter string
	if err := tx.QueryRow("SELECT filter_state FROM items WHERE id=?", itemID).Scan(&filter); err != nil {
		return false, err
	}
	return filter == "rejected", nil
}
