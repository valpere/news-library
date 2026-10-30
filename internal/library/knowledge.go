package library

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const KnowledgePromptVersion = "article-connections-v2"

type ArticleKnowledge struct {
	FileError                      string `json:"file_error,omitempty"`
	ID, RunID, ItemID              int64
	Category                       string   `json:"category"`
	Tags                           []string `json:"tags"`
	Questions                      []string `json:"questions"`
	Markdown, Model, PromptVersion string
}
type KnowledgeRun struct {
	ID, ItemID                                      int64
	Model, PromptVersion, Token, State, Error, Kind string
	Attempts, LinkedCount                           int
}
type KnowledgeContext struct {
	EvidenceVersion            string
	ItemID                     int64
	Title, URL, Text, Coverage string
	Category                   string
	Tags, Questions            []string
	Evidence                   []KnowledgeEvidence
}
type KnowledgeLink struct {
	EvidenceVersion   string   `json:"evidence_version,omitempty"`
	ItemID            int64    `json:"item_id"`
	Title             string   `json:"title"`
	Body              string   `json:"body"`
	Category          string   `json:"category"`
	Tags              []string `json:"tags"`
	Questions         []string `json:"questions"`
	CurrentRole       string   `json:"current_role"`
	OtherRole         string   `json:"other_role"`
	CurrentEvidence   string   `json:"current_evidence"`
	CurrentEvidenceID *int     `json:"current_evidence_id,omitempty"`
	OtherEvidence     string   `json:"other_evidence"`
	OtherEvidenceID   *int     `json:"other_evidence_id,omitempty"`
}

func validateKnowledge(k *ArticleKnowledge) error {
	k.Category = strings.TrimSpace(k.Category)
	if k.Category == "" || !utf8.ValidString(k.Category) || utf8.RuneCountInString(k.Category) > 120 || strings.ContainsAny(k.Category, "\r\n") {
		return errors.New("потрібна коротка категорія статті")
	}
	var err error
	k.Tags, err = connectionList(k.Tags, 12, 80)
	if err != nil {
		return err
	}
	k.Questions, err = connectionList(k.Questions, 10, 300)
	if err != nil {
		return err
	}
	if len(k.Tags) == 0 || len(k.Questions) == 0 {
		return errors.New("потрібні теги та питання до змісту статті")
	}
	return nil
}
func (s *Store) queueKnowledge(tx *sql.Tx, item int64, model string) error {
	_, err := tx.Exec(`INSERT INTO knowledge_runs(item_id,model,prompt_version,updated_at) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM items WHERE id=? AND filter_state IN ('passed','overridden')) ON CONFLICT DO NOTHING`, item, model, KnowledgePromptVersion, nowUTC(), item)
	return err
}
func (s *Store) RetryKnowledge(item int64) error {
	if !s.KnowledgeEnabled || s.DigestConfig == nil {
		return errors.New("автоматичний аналіз вимкнено")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var did int64
	err = tx.QueryRow(`SELECT d.item_id FROM active_items i JOIN digests d ON d.item_id=i.id WHERE i.id=? AND i.filter_state IN ('passed','overridden')`, item).Scan(&did)
	if err != nil {
		return errors.New("спершу потрібен готовий дайджест дозволеного матеріалу")
	}
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM knowledge_runs WHERE item_id=? AND state IN ('queued','running','retry_wait')", item).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return errors.New("аналіз уже активний")
	}
	_, err = tx.Exec(`UPDATE knowledge_runs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='',kind='',updated_at=? WHERE item_id=? AND prompt_version=? AND model=? AND state IN ('failed','cancelled')`, nowUTC(), did, KnowledgePromptVersion, s.DigestConfig.Model)
	if err != nil {
		return err
	}
	if err = s.queueKnowledge(tx, item, s.DigestConfig.Model); err != nil {
		return err
	}
	return tx.Commit()
}

const knowledgeRunSelect = `SELECT id,item_id,model,prompt_version,token,state,error,kind,attempts,linked_count FROM knowledge_runs`

func scanKnowledgeRun(row interface{ Scan(...any) error }) (KnowledgeRun, error) {
	var j KnowledgeRun
	err := row.Scan(&j.ID, &j.ItemID, &j.Model, &j.PromptVersion, &j.Token, &j.State, &j.Error, &j.Kind, &j.Attempts, &j.LinkedCount)
	return j, err
}
func (s *Store) KnowledgeStatus(item int64) (*KnowledgeRun, error) {
	j, err := scanKnowledgeRun(s.DB.QueryRow(knowledgeRunSelect+" WHERE item_id=? ORDER BY updated_at DESC,id DESC LIMIT 1", item))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}
func (s *Store) claimKnowledge(timeout time.Duration, target ...int64) (*KnowledgeRun, error) {
	var itemID int64
	if len(target) > 0 {
		itemID = target[0]
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.Exec(`UPDATE knowledge_runs SET state='cancelled',lease_until=0 WHERE state IN ('queued','running','retry_wait') AND (?=0 OR item_id=?) AND NOT EXISTS(SELECT 1 FROM active_items i WHERE i.id=knowledge_runs.item_id AND i.filter_state IN ('passed','overridden') AND EXISTS(SELECT 1 FROM digests WHERE digests.item_id=i.id))`, itemID, itemID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE knowledge_runs SET state='failed',kind='unknown',error='Аналіз не завершено після перезапуску',lease_until=0 WHERE state='running' AND lease_until<=? AND attempts>=3 AND (?=0 OR item_id=?)`, now, itemID, itemID); err != nil {
		return nil, err
	}
	j, err := scanKnowledgeRun(tx.QueryRow(knowledgeRunSelect+` WHERE ((state IN ('queued','retry_wait') AND next_attempt<=?) OR (state='running' AND lease_until<=? AND attempts<3)) AND (?=0 OR item_id=?) ORDER BY id LIMIT 1`, now, now, itemID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	j.Token = rand.Text()
	j.Attempts++
	_, err = tx.Exec("UPDATE knowledge_runs SET state='running',token=?,attempts=?,lease_until=? WHERE id=?", j.Token, j.Attempts, time.Now().Add(timeout+time.Minute).UnixMilli(), j.ID)
	if err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}
func knowledgeLease(tx *sql.Tx, j KnowledgeRun) error {
	var valid int
	err := tx.QueryRow(`SELECT count(*) FROM knowledge_runs r JOIN active_items i ON i.id=r.item_id WHERE r.id=? AND r.token=? AND r.state='running' AND r.lease_until>? AND i.filter_state IN ('passed','overridden') AND EXISTS(SELECT 1 FROM digests WHERE digests.item_id=r.item_id)`, j.ID, j.Token, time.Now().UnixMilli()).Scan(&valid)
	if err != nil {
		return err
	}
	if valid != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Store) Knowledge(item int64) (*ArticleKnowledge, error) {
	return s.knowledgeRecord(" WHERE k.item_id=? ORDER BY r.updated_at DESC,k.id DESC LIMIT 1", item)
}
func (s *Store) knowledgeForRun(run int64) (*ArticleKnowledge, error) {
	return s.knowledgeRecord(" WHERE k.run_id=?", run)
}
func (s *Store) knowledgeRecord(where string, args ...any) (*ArticleKnowledge, error) {
	var k ArticleKnowledge
	var tags, questions, path, hash string
	err := s.DB.QueryRow(`SELECT k.id,k.run_id,k.item_id,k.category,k.tags_json,k.questions_json,k.markdown_path,k.hash,k.model,k.prompt_version FROM article_knowledge k JOIN knowledge_runs r ON r.id=k.run_id`+where, args...).Scan(&k.ID, &k.RunID, &k.ItemID, &k.Category, &tags, &questions, &path, &hash, &k.Model, &k.PromptVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(tags), &k.Tags); err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(questions), &k.Questions); err != nil {
		return nil, err
	}
	data, err := s.Files.Read(path, hash)
	k.Markdown = string(data)
	if err != nil {
		k.FileError = "Markdown аналізу недоступний; перевірте файли або backup"
	}
	return &k, nil
}
func (s *Store) completeKnowledgeMetadata(j KnowledgeRun, k ArticleKnowledge, d Digest) error {
	if err := validateKnowledge(&k); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = knowledgeLease(tx, j); err != nil {
		return err
	}
	if d.ItemID != j.ItemID {
		return errors.New("analysis digest ownership mismatch")
	}
	var sourceURL, title, sourceHash string
	if err = tx.QueryRow("SELECT i.original_url,v.title,v.hash FROM active_items i JOIN texts v ON v.item_id=i.id WHERE i.id=?", j.ItemID).Scan(&sourceURL, &title, &sourceHash); err != nil {
		return err
	}
	markdown := fmt.Sprintf("---\nschema: news-library/article-v1\ntype: article\ntitle: %s\nurl: %s\nsource_hash: %s\nevidence_scope: saved-digest-and-bounded-source-excerpt\nitem_id: %d\ncategory: %s\ntags: %s\nquestions: %s\naliases: %s\nauthor: model\nmodel: %s\nprompt_version: %s\n---\n\n%s\n", jsonString(title), jsonString(sourceURL), jsonString(sourceHash), j.ItemID, jsonString(k.Category), jsonString(k.Tags), jsonString(k.Questions), jsonString(k.Questions), jsonString(j.Model), jsonString(j.PromptVersion), d.Markdown)
	path, err := s.Files.Put("digests", []byte(markdown))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO article_knowledge(run_id,item_id,category,tags_json,questions_json,search_text,markdown_path,hash,model,prompt_version) VALUES(?,?,?,?,?,?,?,?,?,?)`, j.ID, j.ItemID, k.Category, jsonString(k.Tags), jsonString(k.Questions), strings.ToLower(k.Category+" "+strings.Join(k.Tags, " ")+" "+strings.Join(k.Questions, " ")), path, textHash(markdown), j.Model, j.PromptVersion)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) failKnowledge(j KnowledgeRun, cause error) error {
	f := FailureFromError(cause)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = knowledgeLease(tx, j); err != nil {
		return err
	}
	state := "failed"
	if f.Kind == "temporary" && f.Retryable && j.Attempts < 3 {
		state = "retry_wait"
	}
	path, hash := "", ""
	if f.Response != "" {
		if len(f.Response) > 2<<20 {
			return errors.New("diagnostic too large")
		}
		path, err = s.Files.Put("digests", []byte(f.Response))
		if err != nil {
			return err
		}
		hash = textHash(f.Response)
	}
	// Only the last failure is kept: it replaces the previous one and its diagnostic file.
	var replaced string
	if err = tx.QueryRow("SELECT response_path FROM knowledge_runs WHERE id=?", j.ID).Scan(&replaced); err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE knowledge_runs SET state=?,error=?,kind=?,response_path=?,hash=?,lease_until=0,next_attempt=?,updated_at=? WHERE id=?", state, f.Message, f.Kind, path, hash, time.Now().Add(time.Duration(j.Attempts)*10*time.Second).UnixMilli(), nowUTC(), j.ID)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.removeUnusedFiles(map[string]bool{replaced: replaced != ""}) // best effort
	return nil
}

// KnowledgeFailures returns the last failure of the item's analysis: zero or one element.
func (s *Store) KnowledgeFailures(item int64) ([]JobFailure, error) {
	f := JobFailure{}
	var path, hash string
	err := s.DB.QueryRow(`SELECT id,kind,error,response_path,hash FROM knowledge_runs WHERE item_id=? ORDER BY updated_at DESC,id DESC LIMIT 1`, item).Scan(&f.JobID, &f.Kind, &f.Message, &path, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Only the latest run counts: a newer success supersedes an older failure.
	if f.Kind == "" {
		return nil, nil
	}
	if path != "" {
		data, e := s.Files.Read(path, hash)
		if e != nil {
			f.DiagnosticError = "Діагностична відповідь недоступна"
		} else {
			f.Response = string(data)
		}
	}
	return []JobFailure{f}, nil
}
