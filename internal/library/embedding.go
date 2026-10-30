package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// Embeddings are derived data, separate from immutable sources and judgments.
type Embedder interface {
	Revision(context.Context) (string, error)
	Embed(context.Context, string, string) ([]float64, error)
}
type BGE struct {
	gateway *Ollama
	URL     string
}

func NewBGE(endpoint string, timeout time.Duration) (*BGE, error) {
	if !strings.HasSuffix(endpoint, "/api/embed") {
		return nil, errors.New("embedding endpoint must end in /api/embed")
	}
	o, err := NewOllama(strings.TrimSuffix(endpoint, "/api/embed")+"/api/chat", "", timeout)
	if err != nil {
		return nil, err
	}
	return &BGE{gateway: o, URL: endpoint}, nil
}
func (b *BGE) Revision(ctx context.Context) (string, error) {
	body, err := b.gateway.request(ctx, "GET", strings.TrimSuffix(b.URL, "/api/embed")+"/api/tags", nil)
	if err != nil {
		return "", err
	}
	var inventory struct {
		Models []struct {
			Name, Digest string
			RemoteModel  string `json:"remote_model"`
			Host         string `json:"remote_host"`
		}
	}
	if err = json.Unmarshal(body, &inventory); err != nil {
		return "", errors.New("invalid embedding inventory")
	}
	for _, m := range inventory.Models {
		if (m.Name == "bge-m3:latest" || m.Name == "bge-m3") && m.Digest != "" && m.Host == "" && m.RemoteModel == "" {
			return m.Digest, nil
		}
	}
	return "", errors.New("local bge-m3 is unavailable")
}
func byteBoundary(text string, offset int) bool {
	return offset >= 0 && offset <= len(text) && (offset == len(text) || utf8.RuneStart(text[offset]))
}
func normalized(vector []float64, dimensions int) ([]float64, error) {
	if len(vector) != dimensions {
		return nil, errors.New("embedding dimension mismatch")
	}
	norm := 0.0
	for _, v := range vector {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("non-finite embedding")
		}
		norm += v * v
	}
	if norm <= 0 || math.IsInf(norm, 0) {
		return nil, errors.New("invalid embedding norm")
	}
	out := make([]float64, len(vector))
	norm = math.Sqrt(norm)
	for i, v := range vector {
		out[i] = v / norm
	}
	return out, nil
}
func (b *BGE) Embed(ctx context.Context, text, revision string) ([]float64, error) {
	current, err := b.Revision(ctx)
	if err != nil {
		return nil, err
	}
	if current != revision {
		return nil, errors.New("embedding model revision changed")
	}
	payload, _ := json.Marshal(map[string]any{"model": "bge-m3:latest", "input": text, "truncate": false})
	body, err := b.gateway.request(ctx, "POST", b.URL, payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		Model      string
		Embeddings [][]float64
	}
	if err = json.Unmarshal(body, &result); err != nil || len(result.Embeddings) != 1 || (result.Model != "bge-m3:latest" && result.Model != "bge-m3") {
		return nil, errors.New("invalid bge-m3 response")
	}
	// Do not publish a vector under an inventory revision replaced during inference.
	current, err = b.Revision(ctx)
	if err != nil || current != revision {
		return nil, errors.New("embedding model changed during request")
	}
	return normalized(result.Embeddings[0], 1024)
}

type Semantic struct {
	Store                  *Store
	Embedder               Embedder
	ChunkBytes, Dimensions int
	Timeout                time.Duration
}

func (s *Semantic) version(revision string) string {
	return fmt.Sprintf("bge-m3:%s|utf8-bytes-v1:%d|dense:%d", revision, s.ChunkBytes, s.Dimensions)
}
func (s *Semantic) schedule(revision string) error {
	_, err := s.Store.DB.Exec(`INSERT INTO embedding_runs(item_id,version,model_revision,chunk_bytes,dimensions,created_at)
 SELECT i.id,?,?,?,?,? FROM active_items i WHERE EXISTS(SELECT 1 FROM texts WHERE texts.item_id=i.id) AND i.filter_state IN ('passed','overridden')

 ON CONFLICT DO NOTHING`, s.version(revision), revision, s.ChunkBytes, s.Dimensions, nowUTC())
	return err
}

type embeddingRun struct {
	SnapshotID                             int64
	Version, Revision, Token               string
	Part, Attempts, ChunkBytes, Dimensions int
}

func (s *Semantic) claim(revision string) (*embeddingRun, error) {
	tx, err := s.Store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	_, err = tx.Exec(`UPDATE embedding_runs SET state='failed',error='embedding attempts exhausted after restart',lease_until=0 WHERE state='running' AND lease_until<=? AND attempts>=3`, now)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`UPDATE embedding_runs SET state='cancelled',lease_until=0 WHERE state IN ('queued','retry_wait') AND NOT EXISTS(SELECT 1 FROM active_items i WHERE i.id=embedding_runs.item_id AND i.filter_state IN ('passed','overridden'))`)
	if err != nil {
		return nil, err
	}
	var j embeddingRun
	err = tx.QueryRow(`SELECT item_id,version,model_revision,next_part,attempts,chunk_bytes,dimensions FROM embedding_runs WHERE version=? AND ((state IN ('queued','retry_wait') AND next_attempt<=?) OR (state='running' AND lease_until<=? AND attempts<3)) ORDER BY item_id LIMIT 1`, s.version(revision), now, now).Scan(&j.SnapshotID, &j.Version, &j.Revision, &j.Part, &j.Attempts, &j.ChunkBytes, &j.Dimensions)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	j.Token = rand.Text()
	j.Attempts++
	_, err = tx.Exec(`UPDATE embedding_runs SET state='running',token=?,attempts=?,lease_until=? WHERE item_id=? AND version=?`, j.Token, j.Attempts, time.Now().Add(s.Timeout+time.Minute).UnixMilli(), j.SnapshotID, j.Version)
	if err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}
func embeddingLease(tx *sql.Tx, j embeddingRun) error {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM embedding_runs WHERE item_id=? AND version=? AND token=? AND state='running' AND lease_until>?`, j.SnapshotID, j.Version, j.Token, time.Now().UnixMilli()).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (s *Semantic) finish(j embeddingRun, parts []string, vector []float64) error {
	tx, err := s.Store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = embeddingLease(tx, j); err != nil {
		return err
	}
	var allowed int
	if err = tx.QueryRow(`SELECT count(*) FROM items WHERE id=? AND filter_state IN ('passed','overridden')`, j.SnapshotID).Scan(&allowed); err != nil {
		return err
	}
	if allowed == 0 {
		_, err = tx.Exec(`UPDATE embedding_runs SET state='cancelled',lease_until=0 WHERE item_id=? AND version=?`, j.SnapshotID, j.Version)
	} else {
		start := 0
		for _, part := range parts[:j.Part] {
			start += len(part)
		}
		data, _ := json.Marshal(vector)
		_, err = tx.Exec(`INSERT INTO embedding_chunks(item_id,version,part,start_byte,end_byte,vector) VALUES(?,?,?,?,?,?)`, j.SnapshotID, j.Version, j.Part, start, start+len(parts[j.Part]), string(data))
		if err == nil {
			state := "queued"
			if j.Part+1 == len(parts) {
				state = "done"
			}
			_, err = tx.Exec(`UPDATE embedding_runs SET state=?,next_part=?,total_parts=?,attempts=0,next_attempt=0,lease_until=0,error='' WHERE item_id=? AND version=?`, state, j.Part+1, len(parts), j.SnapshotID, j.Version)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Semantic) Step(ctx context.Context) (bool, error) {
	work, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	revision, err := s.Embedder.Revision(work)
	if err != nil {
		return false, err
	}
	if err = s.schedule(revision); err != nil {
		return false, err
	}
	j, err := s.claim(revision)
	if err != nil || j == nil {
		return false, err
	}
	snap, err := s.Store.Snapshot(j.SnapshotID)
	if err == nil {
		err = ValidateSourceContent(snap)
	}
	var parts []string
	var vector []float64
	if err == nil {
		parts, err = SplitText(snap.Text, j.ChunkBytes)
	}
	if err == nil && (j.Part < 0 || j.Part >= len(parts)) {
		err = errors.New("invalid embedding checkpoint")
	}
	var allowed int
	if err == nil {
		err = s.Store.DB.QueryRow(`SELECT count(*) FROM items WHERE id=? AND filter_state IN ('passed','overridden')`, j.SnapshotID).Scan(&allowed)
	}
	if err == nil && allowed == 0 { // No inference after an already recorded rejection.
		_, err = s.Store.DB.Exec(`UPDATE embedding_runs SET state='cancelled',lease_until=0 WHERE item_id=? AND version=? AND token=?`, j.SnapshotID, j.Version, j.Token)
		return true, err
	}
	if err == nil {
		vector, err = s.Embedder.Embed(work, parts[j.Part], j.Revision)
	}
	if err == nil {
		vector, err = normalized(vector, j.Dimensions)
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	} // Retain lease for recovery on shutdown.
	if err == nil {
		return true, s.finish(*j, parts, vector)
	}
	state := "retry_wait"
	var sourceErr *SourceError
	if j.Attempts >= 3 || (errors.As(err, &sourceErr) && sourceErr.Kind == "source") {
		state = "failed"
	}
	// Generic diagnostic avoids leaking provider data; primary state is untouched.
	result, saveErr := s.Store.DB.Exec(`UPDATE embedding_runs SET state=?,error='embedding failed; retry or check local bge-m3',next_attempt=?,lease_until=0 WHERE item_id=? AND version=? AND token=? AND state='running' AND lease_until>?`, state, time.Now().Add(10*time.Second).UnixMilli(), j.SnapshotID, j.Version, j.Token, time.Now().UnixMilli())
	if saveErr == nil {
		n, e := result.RowsAffected()
		if e != nil {
			saveErr = e
		} else if n != 1 {
			saveErr = ErrLeaseLost
		}
	}
	return true, saveErr
}
func (s *Semantic) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := s.Step(ctx)
		delay := time.Second
		if err != nil {
			delay = 10 * time.Second
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RebuildEmbeddings is an explicit user action. Only derived rows are reset.
func (s *Store) RebuildEmbeddings() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id,id FROM items WHERE EXISTS(SELECT 1 FROM texts WHERE texts.item_id=items.id) AND filter_state='not_checked'")
	if err != nil {
		return err
	}
	var legacy [][2]int64
	for rows.Next() {
		var ids [2]int64
		if err = rows.Scan(&ids[0], &ids[1]); err != nil {
			break
		}
		legacy = append(legacy, ids)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	// A migrated snapshot gets its first recorded rule decision before inference;
	// existing decisions are never rescanned just because configuration changed.
	for _, ids := range legacy {
		if _, err = s.ensureFilterForSnapshot(tx, ids[0], ids[1]); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM embedding_chunks WHERE item_id IN (SELECT id FROM items WHERE filter_state IN ('passed','overridden'))`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE embedding_runs SET state='queued',next_part=0,total_parts=0,attempts=0,next_attempt=0,lease_until=0,token='',error='' WHERE item_id IN (SELECT id FROM items WHERE filter_state IN ('passed','overridden'))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) EmbeddingStatus() string {
	rows, err := s.DB.Query(`SELECT e.state,count(*) FROM embedding_runs e JOIN active_items i ON i.id=e.item_id GROUP BY e.state ORDER BY e.state`)
	if err != nil {
		return "Стан індексації недоступний"
	}
	defer rows.Close()
	labels := map[string]string{"queued": "очікують", "running": "обробляються", "retry_wait": "очікують повтору", "done": "готові", "failed": "помилка", "cancelled": "скасовані"}
	var status []string
	for rows.Next() {
		var state string
		var count int
		if rows.Scan(&state, &count) != nil {
			return "Стан індексації недоступний"
		}
		status = append(status, fmt.Sprintf("%s: %d", labels[state], count))
	}
	if rows.Err() != nil {
		return "Стан індексації недоступний"
	}
	if len(status) == 0 {
		return "Індекс ще не створено"
	}
	return strings.Join(status, " · ")
}
