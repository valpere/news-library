// Explicit recovery of failed analysis. Default mode is read-only and offline.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/valpere/news-library/internal/library"
)

func ratingsHash(db *sql.DB) (string, error) {
	rows, err := db.Query("SELECT id,rating,rated_at FROM items ORDER BY id")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	all := [][]any{}
	for rows.Next() {
		row := make([]any, 3)
		ptr := make([]any, 3)
		for i := range row {
			ptr[i] = &row[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			return "", err
		}
		all = append(all, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(all)
	return fmt.Sprintf("%x", sha256.Sum256(raw)), err
}
func run() error {
	data := flag.String("data", "", "existing data root (required)")
	apply := flag.Bool("apply", false, "explicitly queue and process this batch")
	cloud := flag.Bool("run-cloud", false, "allow cloud generation for replies that cannot be replayed")
	endpoint := flag.String("ollama-url", "http://127.0.0.1:11434/api/chat", "local Ollama cloud gateway")
	limit := flag.Int("limit", 5, "maximum number of jobs, 1..500")
	token := flag.String("request-token", "", "persistent batch token; generated when omitted")
	output := flag.String("output", "", "optional local JSON report")
	flag.Parse()
	if *data == "" || flag.NArg() != 0 || *limit < 1 || *limit > 500 || (*cloud && !*apply) {
		return errors.New("use -data ROOT; mutations require -apply, generation also requires -run-cloud")
	}
	root, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	var s *library.Store
	if *apply {
		if library.RestoredPaused(root) {
			return errors.New("restored workers are paused; resume explicitly before recovery")
		}
		s, err = library.Open(filepath.Join(root, "library.sqlite"))
	} else {
		u := url.URL{Scheme: "file", Path: filepath.Join(root, "library.sqlite")}
		q := u.Query()
		q.Set("mode", "ro")
		u.RawQuery = q.Encode()
		var db *sql.DB
		db, err = sql.Open("sqlite", u.String())
		if err == nil {
			db.SetMaxOpenConns(1)
			s = &library.Store{DB: db, Files: library.Files{Root: root}}
		}
	}
	if err != nil {
		return err
	}
	defer s.Close()
	audit, err := s.AuditKnowledgeRecovery()
	if err != nil {
		return err
	}
	replayable := 0
	for _, a := range audit {
		if a.Replayable {
			replayable++
		}
	}
	incomplete := false
	report := map[string]any{"failed": len(audit), "replayable": replayable, "audit": audit, "apply": *apply, "cloud_allowed": *cloud}
	fmt.Printf("Failed: %d; saved replies replayable now: %d\n", len(audit), replayable)
	if *apply {
		var active int
		if err := s.DB.QueryRow("SELECT count(*) FROM knowledge_runs WHERE state IN ('queued','running','retry_wait')").Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return errors.New("analysis queue already active; refusing unrelated work")
		}
		summary, err := s.KnowledgeBatchSummary()
		if err != nil {
			return err
		}
		if summary.New > 0 {
			return errors.New("new analysis work exists; recovery only resumes failed work, use the normal queue for new items")
		}
		before, err := ratingsHash(s.DB)
		if err != nil {
			return err
		}
		s.KnowledgeEnabled = true
		s.DigestConfig = &library.DigestConfig{} // Required by the batch API; resumed jobs keep their exact model.
		g := &library.KnowledgeRecoveryGenerator{Store: s}
		if *cloud {
			g.Fallback, err = library.NewOllama(*endpoint, os.Getenv("OLLAMA_API_KEY"), 3*time.Minute)
			if err != nil {
				return err
			}
		}
		if *token == "" {
			*token = rand.Text()
		}
		// Preview first. Offline writes must not consume a job needing generation.
		if !*cloud {
			preview := 0
			for _, a := range audit {
				if preview >= *limit {
					break
				}
				if !a.Replayable {
					return errors.New("batch contains non-replayable work; use explicit -run-cloud")
				}
				preview++
			}
		}
		batch, err := s.QueueKnowledgeBatch(*token, *limit, true)
		if err != nil {
			return err
		}
		report["request_token"] = *token
		report["batch"] = batch
		if batch.Replayed {
			return errors.New("batch token already used; inspect its receipt before creating another batch")
		}
		allowed := map[int64]bool{}
		for _, row := range batch.Rows {
			if row.Queued {
				allowed[row.ItemID] = true
			}
		}
		worker := library.KnowledgeWorker{Store: s, Generator: g, Timeout: 3 * time.Minute}
		for step := 0; step < len(allowed); step++ {
			var next int64
			err := s.DB.QueryRow("SELECT item_id FROM knowledge_runs WHERE state='queued' ORDER BY id LIMIT 1").Scan(&next)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			if !allowed[next] {
				return errors.New("another job entered queue; stopping before unrelated work")
			}
			fmt.Printf("Processing item %d (%d/%d)\n", next, step+1, len(allowed))
			worked, err := worker.Step(context.Background())
			if err != nil {
				return err
			}
			if !worked {
				break
			}
			var kind string
			if err := s.DB.QueryRow("SELECT kind FROM knowledge_runs WHERE item_id=? ORDER BY updated_at DESC,id DESC LIMIT 1", next).Scan(&kind); err != nil {
				return err
			}
			if kind == "temporary" || kind == "configuration" || kind == "limit" {
				fmt.Println("Gateway/configuration issue: stopping batch before more requests")
				break
			}
		}
		statuses := []map[string]any{}
		for item := range allowed {
			var state, reason string
			if err := s.DB.QueryRow("SELECT state,error FROM knowledge_runs WHERE item_id=? ORDER BY updated_at DESC,id DESC LIMIT 1", item).Scan(&state, &reason); err != nil {
				return err
			}
			if state != "done" {
				incomplete = true
			}
			statuses = append(statuses, map[string]any{"item": item, "state": state, "reason": reason})
			fmt.Printf("Item %d: %s %s\n", item, state, reason)
		}
		after, err := ratingsHash(s.DB)
		if err != nil {
			return err
		}
		if before != after {
			return errors.New("human rating history changed unexpectedly")
		}
		report["complete"] = !incomplete
		report["statuses"] = statuses
		report["replayed_stages"] = g.Replayed
		report["generation_stages"] = g.Generated
		report["ratings_unchanged"] = true
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if *output != "" {
		if err := os.WriteFile(*output, append(raw, '\n'), 0600); err != nil {
			return err
		}
	}
	if incomplete {
		return errors.New("batch incomplete; inspect saved per-item status before retry")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
