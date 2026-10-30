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
	if err := s.Complete(claim(t, s), SourceText{Title: "T", Text: "Recovered text", Origin: "fetched", Extractor: "fixture", Completeness: "complete"}); err != nil {
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

func TestRetryDigestRefusesWhenDigestExists(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/ready")
	manual(t, s, id, "Ready text")
	completeFixtureDigest(t, s)
	if err := s.RetryDigest(id); err == nil {
		t.Fatal("retry queued a second generation for a ready digest")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=?", id).Scan(&n)
	if n != 0 {
		t.Fatal("job queued", n)
	}
}

func TestInformationalDatesAreGoneFromTheSchema(t *testing.T) {
	s := testStore(t)
	for table, column := range map[string]string{
		"digests": "created_at", "digest_checkpoints": "created_at", "article_knowledge": "created_at",
		"knowledge_runs": "created_at", "knowledge_batches": "created_at",
	} {
		var n int
		if err := s.DB.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, column, n, err)
		}
	}
	for table, column := range map[string]string{"items": "added_at", "texts": "retrieved_at"} {
		var n int
		if err := s.DB.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&n); err != nil || n != 1 {
			t.Fatal("kept date missing", table, column)
		}
	}
}

func TestKnowledgeKeepsOnlyTheLastFailureAndForgetsItAfterSuccess(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	id := knowledgeArticle(t, s, "last-failure")
	fail := func(diagnostic string) string {
		job, err := s.claimKnowledge(time.Second)
		if err != nil || job == nil {
			t.Fatal(job, err)
		}
		if err = s.failKnowledge(*job, &SourceError{Kind: "format", Message: "bad", Diagnostic: diagnostic}); err != nil {
			t.Fatal(err)
		}
		var path string
		s.DB.QueryRow("SELECT response_path FROM knowledge_runs WHERE id=?", job.ID).Scan(&path)
		return path
	}
	first := fail("first rejected reply")
	if err := s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	second := fail("second rejected reply")
	failures, err := s.KnowledgeFailures(id)
	if err != nil || len(failures) != 1 || failures[0].Response != "second rejected reply" {
		t.Fatal(failures, err)
	}
	if first != second {
		if _, err = os.Stat(filepath.Join(s.Files.Root, first)); !os.IsNotExist(err) {
			t.Fatal("replaced diagnostic kept", err)
		}
	}
}
