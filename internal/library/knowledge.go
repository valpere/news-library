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
	FileError                                 string `json:"file_error,omitempty"`
	ID, RunID, ItemID, SnapshotID, DigestID   int64
	Category                                  string   `json:"category"`
	Tags                                      []string `json:"tags"`
	Questions                                 []string `json:"questions"`
	Markdown, Model, PromptVersion, CreatedAt string
}
type KnowledgeRun struct {
	ID, ItemID, SnapshotID, DigestID                int64
	Model, PromptVersion, Token, State, Error, Kind string
	Attempts, LinkedCount                           int
}
type KnowledgeContext struct {
	EvidenceVersion              string
	ItemID, SnapshotID, DigestID int64
	Title, URL, Text, Coverage   string
	Category                     string
	Tags, Questions              []string
	Evidence                     []KnowledgeEvidence
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
func (s *Store) queueKnowledge(tx *sql.Tx, item, snapshot, digest int64, model string) error {
	_, err := tx.Exec(`INSERT INTO knowledge_runs(item_id,snapshot_id,digest_id,model,prompt_version,created_at,updated_at) SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM items WHERE id=? AND selected_snapshot=? AND filter_state IN ('passed','overridden')) ON CONFLICT DO NOTHING`, item, snapshot, digest, model, KnowledgePromptVersion, nowUTC(), nowUTC(), item, snapshot)
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
	var sid, did int64
	err = tx.QueryRow(`SELECT i.selected_snapshot,d.id FROM active_items i JOIN generated_artifacts d ON d.snapshot_id=i.selected_snapshot WHERE i.id=? AND i.filter_state IN ('passed','overridden') ORDER BY d.id DESC LIMIT 1`, item).Scan(&sid, &did)
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
	_, err = tx.Exec(`UPDATE knowledge_runs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='',kind='',updated_at=? WHERE digest_id=? AND prompt_version=? AND model=? AND state IN ('failed','cancelled')`, nowUTC(), did, KnowledgePromptVersion, s.DigestConfig.Model)
	if err != nil {
		return err
	}
	if err = s.queueKnowledge(tx, item, sid, did, s.DigestConfig.Model); err != nil {
		return err
	}
	return tx.Commit()
}

const knowledgeRunSelect = `SELECT id,item_id,snapshot_id,digest_id,model,prompt_version,token,state,error,kind,attempts,linked_count FROM knowledge_runs`

func scanKnowledgeRun(row interface{ Scan(...any) error }) (KnowledgeRun, error) {
	var j KnowledgeRun
	err := row.Scan(&j.ID, &j.ItemID, &j.SnapshotID, &j.DigestID, &j.Model, &j.PromptVersion, &j.Token, &j.State, &j.Error, &j.Kind, &j.Attempts, &j.LinkedCount)
	return j, err
}
func (s *Store) KnowledgeStatus(item, snapshot int64) (*KnowledgeRun, error) {
	j, err := scanKnowledgeRun(s.DB.QueryRow(knowledgeRunSelect+" WHERE item_id=? AND snapshot_id=? ORDER BY updated_at DESC,id DESC LIMIT 1", item, snapshot))
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
	if _, err = tx.Exec(`UPDATE knowledge_runs SET state='cancelled',lease_until=0 WHERE state IN ('queued','running','retry_wait') AND (?=0 OR item_id=?) AND NOT EXISTS(SELECT 1 FROM active_items i WHERE i.id=knowledge_runs.item_id AND i.selected_snapshot=knowledge_runs.snapshot_id AND i.filter_state IN ('passed','overridden') AND knowledge_runs.digest_id=(SELECT max(id) FROM generated_artifacts WHERE snapshot_id=i.selected_snapshot))`, itemID, itemID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO knowledge_failures(run_id,kind,message,recorded_at) SELECT id,'unknown','Аналіз не завершено після перезапуску',? FROM knowledge_runs WHERE state='running' AND lease_until<=? AND attempts>=3 AND (?=0 OR item_id=?)`, nowUTC(), now, itemID, itemID); err != nil {
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
	err := tx.QueryRow(`SELECT count(*) FROM knowledge_runs r JOIN active_items i ON i.id=r.item_id WHERE r.id=? AND r.token=? AND r.state='running' AND r.lease_until>? AND i.selected_snapshot=r.snapshot_id AND i.filter_state IN ('passed','overridden') AND r.digest_id=(SELECT max(id) FROM generated_artifacts WHERE snapshot_id=r.snapshot_id)`, j.ID, j.Token, time.Now().UnixMilli()).Scan(&valid)
	if err != nil {
		return err
	}
	if valid != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Store) Knowledge(item, snapshot int64) (*ArticleKnowledge, error) {
	return s.knowledgeRecord(" WHERE k.item_id=? AND k.snapshot_id=? ORDER BY r.updated_at DESC,k.id DESC LIMIT 1", item, snapshot)
}
func (s *Store) knowledgeForRun(run int64) (*ArticleKnowledge, error) {
	return s.knowledgeRecord(" WHERE k.run_id=?", run)
}
func (s *Store) knowledgeRecord(where string, args ...any) (*ArticleKnowledge, error) {
	var k ArticleKnowledge
	var tags, questions, path, hash string
	err := s.DB.QueryRow(`SELECT k.id,k.run_id,k.item_id,k.snapshot_id,k.digest_id,k.category,k.tags_json,k.questions_json,k.markdown_path,k.hash,k.model,k.prompt_version,k.created_at FROM article_knowledge k JOIN knowledge_runs r ON r.id=k.run_id`+where, args...).Scan(&k.ID, &k.RunID, &k.ItemID, &k.SnapshotID, &k.DigestID, &k.Category, &tags, &questions, &path, &hash, &k.Model, &k.PromptVersion, &k.CreatedAt)
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
	if d.ID != j.DigestID || d.ItemID != j.ItemID || d.SnapshotID != j.SnapshotID {
		return errors.New("analysis digest ownership mismatch")
	}
	var sourceURL, title, sourceHash string
	if err = tx.QueryRow("SELECT i.original_url,v.title,v.hash FROM active_items i JOIN source_versions v ON v.id=? WHERE i.id=? AND v.item_id=i.id", j.SnapshotID, j.ItemID).Scan(&sourceURL, &title, &sourceHash); err != nil {
		return err
	}
	k.CreatedAt = nowUTC()
	markdown := fmt.Sprintf("---\nschema: news-library/article-v1\ntype: article\ntitle: %s\nurl: %s\nsource_hash: %s\nevidence_scope: saved-digest-and-bounded-source-excerpt\nitem_id: %d\nsnapshot_id: %d\ndigest_id: %d\ncategory: %s\ntags: %s\nquestions: %s\naliases: %s\nauthor: model\nmodel: %s\nprompt_version: %s\ncreated_at: %s\n---\n\n%s\n", jsonString(title), jsonString(sourceURL), jsonString(sourceHash), j.ItemID, j.SnapshotID, j.DigestID, jsonString(k.Category), jsonString(k.Tags), jsonString(k.Questions), jsonString(k.Questions), jsonString(j.Model), jsonString(j.PromptVersion), jsonString(k.CreatedAt), d.Markdown)
	path, err := s.Files.Put("digests", []byte(markdown))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO article_knowledge(run_id,item_id,snapshot_id,digest_id,category,tags_json,questions_json,search_text,markdown_path,hash,model,prompt_version,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, j.ItemID, j.SnapshotID, j.DigestID, k.Category, jsonString(k.Tags), jsonString(k.Questions), strings.ToLower(k.Category+" "+strings.Join(k.Tags, " ")+" "+strings.Join(k.Questions, " ")), path, textHash(markdown), j.Model, j.PromptVersion, k.CreatedAt)
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
	if _, err = tx.Exec("INSERT INTO knowledge_failures(run_id,kind,message,recorded_at,response_path,hash) VALUES(?,?,?,?,?,?)", j.ID, f.Kind, f.Message, nowUTC(), path, hash); err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE knowledge_runs SET state=?,error=?,kind=?,lease_until=0,next_attempt=?,updated_at=? WHERE id=?", state, f.Message, f.Kind, time.Now().Add(time.Duration(j.Attempts)*10*time.Second).UnixMilli(), nowUTC(), j.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) KnowledgeFailures(item, snapshot int64) ([]JobFailure, error) {
	rows, err := s.DB.Query(`SELECT f.id,f.run_id,f.kind,f.message,f.recorded_at,f.response_path,f.hash FROM knowledge_failures f JOIN knowledge_runs r ON r.id=f.run_id WHERE r.item_id=? AND r.snapshot_id=? ORDER BY f.id DESC LIMIT 20`, item, snapshot)
	if err != nil {
		return nil, err
	}
	type record struct {
		failure    JobFailure
		path, hash string
	}
	records := []record{}
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.failure.ID, &r.failure.JobID, &r.failure.Kind, &r.failure.Message, &r.failure.RecordedAt, &r.path, &r.hash); err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []JobFailure{}
	for _, r := range records {
		if r.path != "" {
			data, e := s.Files.Read(r.path, r.hash)
			if e != nil {
				r.failure.DiagnosticError = "Діагностична відповідь недоступна"
			} else {
				r.failure.Response = string(data)
			}
		}
		out = append(out, r.failure)
	}
	return out, nil
}
