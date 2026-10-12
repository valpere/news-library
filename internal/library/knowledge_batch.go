package library

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const KnowledgeBatchLimit = 500

type KnowledgeBatchSummary struct{ New, Continue, Active, Done, Blocked int }
type KnowledgeBatchRow struct {
	ItemID  int64
	Queued  bool
	Resumed bool
	Reason  string
}
type KnowledgeBatchResult struct {
	Rows                  []KnowledgeBatchRow
	New, Resumed, Skipped int
	Replayed              bool
}
type knowledgeBatchCandidate struct {
	ItemID, SnapshotID, DigestID, RunID    int64
	Title, URL, State, Kind, Model, Prompt string
	Checkpoint, Done, Active               bool
	Error                                  string
	Failures                               int
}

const knowledgeBatchSelection = `SELECT i.id,i.selected_snapshot,d.id,i.title,i.original_url,coalesce(r.id,0),coalesce(r.state,''),coalesce(r.kind,''),coalesce(r.model,''),coalesce(r.prompt_version,''),EXISTS(SELECT 1 FROM article_knowledge k WHERE k.run_id=r.id),EXISTS(SELECT 1 FROM knowledge_runs k WHERE k.digest_id=d.id AND k.state='done'),EXISTS(SELECT 1 FROM knowledge_runs k WHERE k.item_id=i.id AND k.state IN ('queued','running','retry_wait')),coalesce(r.error,''),(SELECT count(*) FROM knowledge_failures f WHERE f.run_id=r.id) FROM items i JOIN generated_artifacts d ON d.id=(SELECT max(id) FROM generated_artifacts WHERE snapshot_id=i.selected_snapshot) LEFT JOIN knowledge_runs r ON r.id=(SELECT id FROM knowledge_runs WHERE digest_id=d.id ORDER BY updated_at DESC,id DESC LIMIT 1) WHERE i.filter_state IN ('passed','overridden') ORDER BY i.id`

func knowledgeBatchCandidates(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) ([]knowledgeBatchCandidate, error) {
	rows, err := q.Query(knowledgeBatchSelection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []knowledgeBatchCandidate{}
	for rows.Next() {
		var c knowledgeBatchCandidate
		if err = rows.Scan(&c.ItemID, &c.SnapshotID, &c.DigestID, &c.Title, &c.URL, &c.RunID, &c.State, &c.Kind, &c.Model, &c.Prompt, &c.Checkpoint, &c.Done, &c.Active, &c.Error, &c.Failures); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func knowledgeBatchClass(c knowledgeBatchCandidate) string {
	if c.Done {
		return "done"
	}
	if c.Active {
		return "active"
	}
	if c.RunID == 0 {
		return "new"
	}
	if c.Prompt != KnowledgePromptVersion || !cloudModelID(c.Model) {
		return "blocked"
	}
	if c.State == "cancelled" {
		return "continue"
	}
	if c.State == "failed" {
		switch c.Kind {
		case "format", "temporary", "unknown", "":
			return "continue"
		}
	}
	return "blocked"
}
func (s *Store) KnowledgeBatchSummary() (KnowledgeBatchSummary, error) {
	candidates, err := knowledgeBatchCandidates(s.DB)
	var summary KnowledgeBatchSummary
	for _, c := range candidates {
		switch knowledgeBatchClass(c) {
		case "done":
			summary.Done++
		case "active":
			summary.Active++
		case "new":
			summary.New++
		case "continue":
			summary.Continue++
		default:
			summary.Blocked++
		}
	}
	return summary, err
}

// One receipt per explicit submission makes browser resends idempotent, even if
// the worker finishes (or fails again) before the same POST reaches the server.
func (s *Store) QueueKnowledgeBatch(token string, limit int, includeRetry bool) (KnowledgeBatchResult, error) {
	var result KnowledgeBatchResult
	if limit < 1 || limit > KnowledgeBatchLimit || len(token) < 20 || len(token) > 128 || strings.Trim(token, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return result, errors.New("некоректний пакет; відкрийте сторінку дообробки знову")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var stored string
	var storedLimit int
	var storedRetry bool
	err = tx.QueryRow("SELECT batch_limit,include_retry,result_json FROM knowledge_batches WHERE request_token=?", token).Scan(&storedLimit, &storedRetry, &stored)
	if err == nil {
		if storedLimit != limit || storedRetry != includeRetry {
			return result, errors.New("параметри повторного пакета змінилися; відкрийте сторінку знову")
		}
		if err = json.Unmarshal([]byte(stored), &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if !s.KnowledgeEnabled || s.DigestConfig == nil {
		return result, errors.New("автоматичний аналіз вимкнено")
	}
	candidates, err := knowledgeBatchCandidates(tx)
	if err != nil {
		return result, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return knowledgeBatchClass(candidates[i]) == "new" && knowledgeBatchClass(candidates[j]) != "new"
	})
	considered := 0
	for _, c := range candidates {
		class := knowledgeBatchClass(c)
		if class != "new" && !(class == "continue" && includeRetry) {
			continue
		}
		if result.New+result.Resumed >= limit || considered >= KnowledgeBatchLimit {
			break
		}
		considered++
		row := KnowledgeBatchRow{ItemID: c.ItemID}
		var sourcePath, sourceHash, digestPath, digestHash string
		err = tx.QueryRow(`SELECT v.text_path,v.hash,d.markdown_path,d.hash FROM source_versions v JOIN generated_artifacts d ON d.snapshot_id=v.id WHERE v.id=? AND d.id=?`, c.SnapshotID, c.DigestID).Scan(&sourcePath, &sourceHash, &digestPath, &digestHash)
		if err != nil {
			return result, err
		}
		if _, err = s.Files.Read(sourcePath, sourceHash); err == nil {
			_, err = s.Files.Read(digestPath, digestHash)
		}
		if err == nil && c.Checkpoint {
			var checkpointPath, checkpointHash string
			if err = tx.QueryRow("SELECT markdown_path,hash FROM article_knowledge WHERE run_id=?", c.RunID).Scan(&checkpointPath, &checkpointHash); err != nil {
				return result, err
			}
			_, err = s.Files.Read(checkpointPath, checkpointHash)
		}
		if err != nil {
			row.Reason = "Збережений текст, дайджест або метадані недоступні; перевірте файли чи backup"
			result.Skipped++
			result.Rows = append(result.Rows, row)
			continue
		}
		if class == "new" {
			if err = s.queueKnowledge(tx, c.ItemID, c.SnapshotID, c.DigestID, s.DigestConfig.Model); err != nil {
				return result, err
			}
			result.New++
		} else {
			// Keep the exact run/model/checkpoint. Reconfiguration must not erase work.
			if _, err = tx.Exec(`UPDATE knowledge_runs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='',kind='',updated_at=? WHERE id=?`, nowUTC(), c.RunID); err != nil {
				return result, err
			}
			row.Resumed = true
			result.Resumed++
		}
		row.Queued = true
		result.Rows = append(result.Rows, row)
	}
	if result.Rows == nil {
		result.Rows = []KnowledgeBatchRow{}
	}
	_, err = tx.Exec("INSERT INTO knowledge_batches(request_token,batch_limit,include_retry,created_at,result_json) VALUES(?,?,?,?,?)", token, limit, includeRetry, nowUTC(), jsonString(result))
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (w *Web) knowledgeBatchPage(rw http.ResponseWriter, r *http.Request) {
	summary, err := w.Store.KnowledgeBatchSummary()
	if err != nil {
		http.Error(rw, "стан дообробки недоступний", 503)
		return
	}
	w.render(rw, "knowledgeBatch", pageData{KnowledgeBatchSummary: summary, KnowledgeBatchToken: rand.Text(), KnowledgeEnabled: w.Store.KnowledgeEnabled && w.Store.DigestConfig != nil})
}
func (w *Web) knowledgeBatchSubmit(rw http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.PostForm.Get("limit"))
	if err != nil {
		http.Error(rw, "некоректний розмір пакета", 400)
		return
	}
	include := r.PostForm.Get("include_retry") == "1"
	result, err := w.Store.QueueKnowledgeBatch(r.PostForm.Get("request_token"), limit, include)
	if err != nil {
		http.Error(rw, err.Error(), 400)
		return
	}
	w.render(rw, "knowledgeBatchResult", pageData{KnowledgeBatchResult: &result, KnowledgeBatchToken: rand.Text(), KnowledgeEnabled: w.Store.KnowledgeEnabled && w.Store.DigestConfig != nil})
}
