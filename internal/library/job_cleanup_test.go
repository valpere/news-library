package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func failedFetch(t *testing.T, s *Store, id int64, diagnostic string) Job {
	t.Helper()
	j := claim(t, s)
	err := s.FailError(j, &SourceError{Kind: "format", Message: "bad " + diagnostic, Diagnostic: diagnostic}, false, 3, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestLaterFailureReplacesEarlierAndRemovesItsFile(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/replace")
	first := failedFetch(t, s, id, "first reply")
	var oldPath string
	s.DB.QueryRow("SELECT response_path FROM jobs WHERE id=?", first.ID).Scan(&oldPath)
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	second := claim(t, s)
	if err := s.FailError(second, &SourceError{Kind: "format", Message: "bad second", Diagnostic: "second reply"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	failures, err := s.JobFailures(second.ID)
	if err != nil || len(failures) != 1 || failures[0].Response != "second reply" {
		t.Fatal(failures, err)
	}
	if _, err = os.Stat(filepath.Join(s.Files.Root, oldPath)); !os.IsNotExist(err) {
		t.Fatal("replaced diagnostic kept", err)
	}
}

func TestSuccessfulFetchDeletesJobAndFailure(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/recover")
	first := failedFetch(t, s, id, "rejected reply")
	var path string
	s.DB.QueryRow("SELECT response_path FROM jobs WHERE id=?", first.ID).Scan(&path)
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(claim(t, s), Snapshot{Title: "T", Text: "Recovered text", Origin: "fetched", Extractor: "fixture", Completeness: "complete"}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=?", id).Scan(&n)
	if n != 0 {
		t.Fatal("finished job kept", n)
	}
	if _, err := os.Stat(filepath.Join(s.Files.Root, path)); !os.IsNotExist(err) {
		t.Fatal("failure diagnostic kept after success", err)
	}
}

func TestManualTextDeletesFailedFetchAndItsFailure(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/manual-after-failure")
	failedFetch(t, s, id, "rejected reply")
	manual(t, s, id, "Pasted text")
	var n int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND stage='fetch'", id).Scan(&n)
	if n != 0 {
		t.Fatal("failed fetch kept after manual text", n)
	}
}
