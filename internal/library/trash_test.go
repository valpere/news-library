package library

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrashMissingPageIsSeparateAndDeduplicated(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/missing")
	j := claim(t, s)
	if err := s.FailError(j, &SourceError{Message: "джерело повернуло HTTP 404"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Trash()
	if err != nil || len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("trash %v %v", entries, err)
	}
	if _, err = s.Item(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("active item: %v", err)
	}
	items, err := s.Search(Filters{})
	if err != nil || len(items) != 0 {
		t.Fatalf("search %v %v", items, err)
	}
	items, err = s.Search(Filters{Query: "missing"})
	if err != nil || len(items) != 0 {
		t.Fatalf("fts %v %v", items, err)
	}
	results, err := s.AddURLs("https://example.com/missing?utm_source=repeat")
	if err != nil || len(results) != 1 || results[0].Status != "trashed" || results[0].ItemID != id {
		t.Fatalf("duplicate %v %v", results, err)
	}
	if err = s.Retry(id); err == nil {
		t.Fatal("trash requeued")
	}
	if err = s.Manual(id, "manual source"); err == nil {
		t.Fatal("trash accepted normal manual input")
	}
	if err = s.SetRead(id, true); err == nil {
		t.Fatal("trash accepts normal mutation")
	}
	if err = s.Rate(id, 3, nil, nil, ""); err == nil {
		t.Fatal("trash accepts rating")
	}
	next, err := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if err != nil || next != nil {
		t.Fatalf("worker %v %v", next, err)
	}
	failures, err := s.JobFailures(j.ID)
	if err != nil || len(failures) != 1 {
		t.Fatalf("history %v %v", failures, err)
	}
	w, err := NewWeb(s, "http://localhost:8787", 10000)
	if err != nil {
		t.Fatal(err)
	}
	session, addReply := chatTurn(t, w, ChatSession{}, "https://example.com/missing", "", "")
	_ = session
	if len(addReply.Cards) != 0 || !strings.Contains(addReply.Message, "помийці") {
		t.Fatalf("chat duplicate %v", addReply)
	}
	reply, err := w.chatProcessingReply(false)
	if err != nil || !strings.Contains(reply.Message, "залишаються 0 матеріалів") {
		t.Fatalf("counts %v %v", reply, err)
	}
	req := httptest.NewRequest("GET", "http://localhost:8787/trash", nil)
	rw := httptest.NewRecorder()
	w.Handler().ServeHTTP(rw, req)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "https://example.com/missing") {
		t.Fatalf("trash page %d %s", rw.Code, rw.Body.String())
	}
}

func TestTrashPreservesRecoverableAndSavedSources(t *testing.T) {
	for _, tc := range []struct {
		name, url, message string
		saved              bool
	}{
		{"rate-limit", "https://example.com/limited", "джерело повернуло HTTP 429", false},
		{"subtitles", "https://www.youtube.com/watch?v=abcdefghijk", "субтитри недоступні або не вдалося їх прочитати", false},
		{"youtube-404", "https://www.youtube.com/watch?v=abcdefghijk", "джерело повернуло HTTP 404", false},
		{"existing-source", "https://example.com/saved", "джерело повернуло HTTP 404", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			id := add(t, s, tc.url)
			j := claim(t, s)
			if tc.saved {
				manual(t, s, id, "original text remains")
			}
			if err := s.Fail(j, tc.message, false, 3, time.Now(), 0); err != nil {
				t.Fatal(err)
			}
			entries, err := s.Trash()
			if err != nil || len(entries) != 0 {
				t.Fatalf("trash %v %v", entries, err)
			}
			if _, err = s.Item(id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTrashMigrationAndBackup(t *testing.T) {
	s := backupStore(t)
	id := add(t, s, "https://example.com/gone")
	j := claim(t, s)
	if err := s.Fail(j, "джерело повернуло HTTP 410", false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	// Emulate v14 with completed historic failure.
	if _, err := s.DB.Exec("DROP TABLE completeness_confirmations; DROP TABLE search_judgments; DROP TABLE search_events; DROP VIEW active_items; DROP TRIGGER IF EXISTS immutable_snapshot_delete; DROP TRIGGER IF EXISTS immutable_artifact_delete; DROP TRIGGER IF EXISTS immutable_rating_delete; DROP TRIGGER IF EXISTS immutable_filter_delete; DROP TRIGGER IF EXISTS immutable_feedback_delete; DROP TRIGGER IF EXISTS immutable_prediction_delete; DROP TRIGGER IF EXISTS immutable_checkpoint_delete; DROP TRIGGER IF EXISTS immutable_job_failure_delete; DROP TRIGGER IF EXISTS immutable_article_knowledge_delete; DROP TRIGGER IF EXISTS immutable_knowledge_failure_delete; DROP TABLE trash_entries; PRAGMA user_version=14"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Files.Root, "library.sqlite")
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err := reopened.Trash()
	if err != nil || len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("migration %v %v", entries, err)
	}
	var integrity string
	if err = reopened.DB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %s %v", integrity, err)
	}
}

func TestTrashSurvivesBackupRestore(t *testing.T) {
	s := backupStore(t)
	id := add(t, s, "https://example.com/404")
	j := claim(t, s)
	if err := s.FailError(j, &SourceError{Message: "джерело повернуло HTTP 404"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	target := filepath.Join(root, "restore")
	if _, err := Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), backup, target); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(target, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	rows, err := restored.AddURLs("https://example.com/404")
	if err != nil || len(rows) != 1 || rows[0].Status != "trashed" || rows[0].ItemID != id {
		t.Fatalf("restored %v %v", rows, err)
	}
	if !RestoredPaused(target) {
		t.Fatal("restore lost pause marker")
	}
}
