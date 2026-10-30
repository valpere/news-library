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
	// MinSourceWords: sources shorter than this get no digest (0 = no cutoff).
	// Not part of Version(): it decides whether a job exists, not how it runs.
	MinSourceWords int
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
	ItemID                                                int64
	Markdown, Model, ActualModel, Provider, PromptVersion string
	InputTokens, OutputTokens                             int64
	Prediction                                            *InterestPrediction
	Feedback                                              string
	ChunkCount                                            int
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
	rows, err := tx.Query(`SELECT id,id FROM items WHERE EXISTS(SELECT 1 FROM texts WHERE texts.item_id=items.id) AND NOT EXISTS(SELECT 1 FROM digests WHERE digests.item_id=items.id) AND NOT EXISTS(SELECT 1 FROM jobs WHERE stage='digest' AND jobs.item_id=items.id)`)
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
	rejected, err := s.ensureFilter(tx, itemID, snapshotID)
	if err != nil || rejected {
		return err
	}
	c := s.DigestConfig
	prompt, interests, chunkLimit, chunkMaxParts := c.PromptVersion, c.Interests, c.ChunkLimit, c.ChunkMaxParts
	if c.MinSourceWords > 0 {
		text, err := snapshotText(tx, s.Files, snapshotID)
		if err != nil {
			return err
		}
		if len(strings.Fields(text)) < c.MinSourceWords {
			// Tiny source: no digest. A foreign-language one is translated.
			if !needsUkrainianTranslation(text) {
				_, err = tx.Exec(`UPDATE items SET state='ready',error='' WHERE id=? AND state NOT IN ('needs_input')`, itemID)
				return err
			}
			prompt, interests, chunkLimit, chunkMaxParts = TranslationPromptVersion, "", 0, 0
		}
	}
	_, err = tx.Exec(`INSERT INTO jobs(item_id,stage,config_version,model,prompt_version,input_limit,interests,chunk_limit,chunk_max_parts) VALUES(?,'digest',?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, itemID, c.Version(), c.Model, prompt, c.MaxInputBytes, interests, chunkLimit, chunkMaxParts)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE items SET state='enriching',error='' WHERE id=? AND NOT EXISTS(SELECT 1 FROM digests WHERE item_id=?)`, itemID, itemID)
	return err
}

// snapshotText reads a stored source inside the caller's transaction (the pool
// has one connection, so s.DB must not be used here).
func snapshotText(tx *sql.Tx, f Files, snapshotID int64) (string, error) {
	var path, hash string
	if err := tx.QueryRow(`SELECT text_path,hash FROM texts WHERE item_id=?`, snapshotID).Scan(&path, &hash); err != nil {
		return "", err
	}
	data, err := f.Read(path, hash)
	return string(data), err
}
func (s *Store) SourceText(id int64) (SourceText, error) {
	v, path, err := scanText(s.DB.QueryRow(textSelect+` WHERE v.item_id=?`, id))
	if err != nil {
		return v, err
	}
	data, err := s.Files.Read(path, v.Hash)
	v.Text = string(data)
	return v, err
}
func (s *Store) CompleteDigest(j Job, d Digest) error {
	if j.Stage != "digest" {
		return errors.New("not a digest job")
	}
	translation := isTranslationPrompt(j.PromptVersion)
	if translation {
		if err := validateTranslation(d.Markdown); err != nil {
			return err
		}
	} else if _, err := SplitDigest(d.Markdown); err != nil {
		return err
	}
	if translation && j.Interests != "" {
		return errors.New("translation has no interest prediction")
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
	erased, err := s.eraseDigest(tx, j.ItemID, j.ID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO digests(item_id,markdown_path,model,actual_model,provider,prompt_version,input_tokens,output_tokens,hash,chunk_count) VALUES(?,?,?,?,?,?,?,?,?,?)`, j.ItemID, path, j.Model, d.ActualModel, d.Provider, j.PromptVersion, d.InputTokens, d.OutputTokens, hash, max(d.ChunkCount, 1)); err != nil {
		return err
	}
	if d.Prediction != nil {
		p := d.Prediction
		if _, err = tx.Exec("INSERT INTO interest_predictions(item_id,probability,reason,profile,profile_hash) VALUES(?,?,?,?,?)", j.ItemID, p.Probability, p.Reason, j.Interests, profileHash(j.Interests)); err != nil {
			return err
		}
	}
	if s.KnowledgeEnabled && !translation {
		if err = s.queueKnowledge(tx, j.ItemID, j.Model); err != nil {
			return err
		}
	}
	finished, err := finishJob(tx, j.ID)
	if err != nil {
		return err
	}
	for p := range finished {
		erased[p] = true
	}
	if _, err = tx.Exec(`UPDATE items SET state='ready',error='' WHERE id=?`, j.ItemID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.removeUnusedFiles(erased) // best effort: the rows are already gone
	return nil
}

const digestSelect = `SELECT item_id,markdown_path,model,actual_model,provider,prompt_version,input_tokens,output_tokens,hash,chunk_count,feedback FROM digests`

func (s *Store) scanDigest(row interface{ Scan(...any) error }) (Digest, error) {
	var d Digest
	var path, hash string
	err := row.Scan(&d.ItemID, &path, &d.Model, &d.ActualModel, &d.Provider, &d.PromptVersion, &d.InputTokens, &d.OutputTokens, &hash, &d.ChunkCount, &d.Feedback)
	if err != nil {
		return d, err
	}
	data, err := s.Files.Read(path, hash)
	d.Markdown = string(data)
	return d, err
}
func (s *Store) DigestOf(id int64) (*Digest, error) {
	d, e := s.scanDigest(s.DB.QueryRow(digestSelect+" WHERE item_id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e == nil {
		d.Prediction, e = s.Prediction(d.ItemID)
	}
	return &d, e
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
	_, err = s.retryDigestTx(tx, itemID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) retryDigestTx(tx *sql.Tx, itemID int64) (bool, error) {
	var err error

	var sid sql.NullInt64
	if err = tx.QueryRow("SELECT (SELECT item_id FROM texts WHERE texts.item_id=items.id) FROM items WHERE id=?", itemID).Scan(&sid); err != nil {
		return false, err
	}
	if !sid.Valid {
		return false, errors.New("спершу потрібен текст")
	}
	var filtered string
	if err = tx.QueryRow("SELECT filter_state FROM items WHERE id=?", itemID).Scan(&filtered); err != nil {
		return false, err
	}
	if filtered == "rejected" {
		return false, errors.New("спершу явно поверніть матеріал до обробки")
	}
	rejected, e := s.ensureFilter(tx, itemID, sid.Int64)
	if e != nil {
		return false, e
	}
	if rejected {
		return false, nil
	}
	var ready int
	// A digest made with the current model and prompt is ready; a changed configuration may regenerate it.
	if err = tx.QueryRow("SELECT count(*) FROM digests WHERE item_id=? AND model=? AND prompt_version=?", sid, s.DigestConfig.Model, s.DigestConfig.PromptVersion).Scan(&ready); err != nil {
		return false, err
	}
	if ready > 0 {
		return false, errors.New("дайджест уже готовий")
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE stage='digest' AND item_id=? AND state IN ('queued','running','retry_wait')", sid).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, errors.New("генерація вже активна")
	}
	c := s.DigestConfig
	var state string
	err = tx.QueryRow("SELECT state FROM jobs WHERE stage='digest' AND item_id=? AND config_version=?", sid, c.Version()).Scan(&state)
	if err == nil {
		if state != "failed" && state != "cancelled" {
			return false, errors.New("дайджест уже готовий або генерація активна")
		}
		if _, err = tx.Exec("UPDATE jobs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='' WHERE stage='digest' AND item_id=? AND config_version=?", sid, c.Version()); err != nil {
			return false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err = s.queueDigest(tx, itemID, sid.Int64); err != nil {
		return false, err
	}
	return true, nil
}

// An item whose text was never checked by the rules is evaluated before its first
// generation; configuration changes leave existing verdicts alone.
func (s *Store) ensureFilter(tx *sql.Tx, itemID, snapshotID int64) (bool, error) {
	var checked string
	if err := tx.QueryRow("SELECT filter_state FROM items WHERE id=?", itemID).Scan(&checked); err != nil {
		return false, err
	}
	if checked == "not_checked" {
		var snap SourceText
		var path, hash string
		if err := tx.QueryRow("SELECT text_path,hash,title FROM texts WHERE item_id=?", itemID).Scan(&path, &hash, &snap.Title); err != nil {
			return false, err
		}
		data, err := s.Files.Read(path, hash)
		if err != nil {
			return false, err
		}
		snap.Text = string(data)
		snap.ItemID = snapshotID
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
