package library

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func batchFixture(t *testing.T) (*Store, *KnowledgeWorker) {
	t.Helper()
	s := backupStore(t)
	enableDigests(t, s)
	for i := 0; i < 7; i++ {
		knowledgeArticle(t, s, fmt.Sprintf("batch-%d", i))
	}
	s.KnowledgeEnabled = true
	return s, &KnowledgeWorker{Store: s, Generator: &fixtureKnowledgeGenerator{}, Timeout: time.Second}
}
func TestKnowledgeBatchProgressAndResendAfterCompletion(t *testing.T) {
	s, w := batchFixture(t)
	summary, err := s.KnowledgeBatchSummary()
	if err != nil || summary.New != 7 {
		t.Fatal(summary, err)
	}
	token := strings.Repeat("a", 26)
	first, err := s.QueueKnowledgeBatch(token, 5, true)
	if err != nil || first.New != 5 || first.Resumed != 0 {
		t.Fatal(first, err)
	}
	for range 5 {
		knowledgeStep(t, w)
	}
	summary, err = s.KnowledgeBatchSummary()
	if err != nil || summary.Done != 5 || summary.New != 2 || summary.Active != 0 {
		t.Fatal(summary, err)
	}
	calls := w.Generator.(*fixtureKnowledgeGenerator).MetadataCalls
	repeated, err := s.QueueKnowledgeBatch(token, 5, true)
	if err != nil || !repeated.Replayed || repeated.New != 5 {
		t.Fatal(repeated, err)
	}
	if worked, err := w.Step(context.Background()); err != nil || worked || w.Generator.(*fixtureKnowledgeGenerator).MetadataCalls != calls {
		t.Fatal(worked, err)
	}
	second, err := s.QueueKnowledgeBatch(strings.Repeat("b", 26), 500, true)
	if err != nil || second.New != 2 {
		t.Fatal(second, err)
	}
	for range 2 {
		knowledgeStep(t, w)
	}
	// Changing configuration must not restart completed work from the batch button.
	s.DigestConfig.Model = "other:cloud"
	empty, err := s.QueueKnowledgeBatch(strings.Repeat("c", 26), 500, true)
	if err != nil || empty.New != 0 || empty.Resumed != 0 {
		t.Fatal(empty, err)
	}
	var receipts int
	s.DB.QueryRow("SELECT count(*) FROM knowledge_batches").Scan(&receipts)
	if receipts != 3 {
		t.Fatal(receipts)
	}
}
func TestKnowledgeBatchResumesPinnedCheckpointAndPreservesFailures(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	knowledgeArticle(t, s, "seed")
	knowledgeStep(t, w)
	id := knowledgeArticle(t, s, "unfinished")
	g.FailLinks = true
	knowledgeStep(t, w)
	item, _ := s.Item(id)
	original, _ := s.Knowledge(id, item.Selected.Int64)
	s.DigestConfig.Model = "other:cloud"
	result, err := s.QueueKnowledgeBatch(strings.Repeat("d", 26), 500, true)
	if err != nil || result.Resumed != 1 || result.New != 0 {
		t.Fatal(result, err)
	}
	g.FailLinks = false
	knowledgeStep(t, w)
	k, err := s.Knowledge(id, item.Selected.Int64)
	if err != nil || k.ID != original.ID || k.Model != "fixture:cloud" || g.MetadataCalls != 2 {
		t.Fatal(k, g, err)
	}
	failures, err := s.KnowledgeFailures(id, item.Selected.Int64)
	if err != nil || len(failures) != 1 || failures[0].Kind != "format" {
		t.Fatal(failures, err)
	}
}
func TestKnowledgeBatchSkipsActiveAndBlockedAndPrioritizesNew(t *testing.T) {
	s, w := batchFixture(t)
	first, err := s.QueueKnowledgeBatch(strings.Repeat("e", 26), 1, false)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.claimKnowledge(time.Second)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	if err = s.failKnowledge(*j, &SourceError{Kind: "format", Message: "fixture format"}); err != nil {
		t.Fatal(err)
	}
	next, err := s.QueueKnowledgeBatch(strings.Repeat("f", 26), 1, true)
	if err != nil || next.New != 1 || next.Resumed != 0 || next.Rows[0].ItemID == first.Rows[0].ItemID {
		t.Fatal(next, err)
	}
	j, err = s.claimKnowledge(time.Second)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	if err = s.failKnowledge(*j, &SourceError{Kind: "configuration", Message: "fixture config"}); err != nil {
		t.Fatal(err)
	}
	summary, err := s.KnowledgeBatchSummary()
	if err != nil || summary.Continue != 1 || summary.Blocked != 1 || summary.New != 5 {
		t.Fatal(summary, err)
	}
	result, err := s.QueueKnowledgeBatch(strings.Repeat("g", 26), 500, false)
	if err != nil || result.New != 5 || result.Resumed != 0 {
		t.Fatal(result, err)
	}
	another, err := s.QueueKnowledgeBatch(strings.Repeat("h", 26), 500, true)
	if err != nil || another.New != 0 || another.Resumed != 1 {
		t.Fatal(another, err)
	}
	final, err := s.QueueKnowledgeBatch(strings.Repeat("i", 26), 500, true)
	if err != nil || final.New != 0 || final.Resumed != 0 {
		t.Fatal(final, err)
	}
	_ = w
}
func TestKnowledgeBatchValidationAndMissingFiles(t *testing.T) {
	s, _ := batchFixture(t)
	for _, limit := range []int{0, 501} {
		if _, err := s.QueueKnowledgeBatch(strings.Repeat("j", 26), limit, true); err == nil {
			t.Fatal("bad limit accepted")
		}
	}
	var queued int
	s.DB.QueryRow("SELECT count(*) FROM knowledge_runs").Scan(&queued)
	if queued != 0 {
		t.Fatal(queued)
	}
	var path string
	s.DB.QueryRow("SELECT text_path FROM source_versions WHERE item_id=1").Scan(&path)
	if err := os.Remove(filepath.Join(s.Files.Root, path)); err != nil {
		t.Fatal(err)
	}
	// Sources share one immutable file: every row is skipped rather than fetched.
	result, err := s.QueueKnowledgeBatch(strings.Repeat("k", 26), 500, true)
	if err != nil || result.Skipped != 7 || result.New != 0 {
		t.Fatal(result, err)
	}
	s.DB.QueryRow("SELECT count(*) FROM knowledge_runs").Scan(&queued)
	if queued != 0 {
		t.Fatal(queued)
	}
	if _, err = s.QueueKnowledgeBatch(strings.Repeat("k", 26), 5, true); err == nil {
		t.Fatal("receipt parameters changed")
	}
}
func TestKnowledgeBatchReceiptRestoreAndWebGuards(t *testing.T) {
	s, _ := batchFixture(t)
	w, err := NewWeb(s, "http://192.0.2.1:8787", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	page := webRequest(w, "GET", "/knowledge-batch", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Ще не почато: 7") {
		t.Fatal(page.Code, page.Body.String())
	}
	values := url.Values{"request_token": {strings.Repeat("m", 26)}, "limit": {"5"}, "include_retry": {"1"}}
	if response := webRequest(w, "POST", "/knowledge-batch", values, nil); response.Code != 403 {
		t.Fatal(response.Code)
	}
	values.Set("csrf", w.CSRF)
	if response := webRequest(w, "POST", "/knowledge-batch", values, map[string]string{"Authorization": "Bearer agent"}); response.Code != 403 {
		t.Fatal(response.Code)
	}
	response := webRequest(w, "POST", "/knowledge-batch", values, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Нових задач: 5") {
		t.Fatal(response.Code, response.Body.String())
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	restoredRoot := filepath.Join(root, "restored")
	if _, err = Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err = Restore(context.Background(), backup, restoredRoot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(restoredRoot, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	// Receipt replay works without enabling inference or consuming restored queues.
	result, err := restored.QueueKnowledgeBatch(strings.Repeat("m", 26), 5, true)
	if err != nil || !result.Replayed || result.New != 5 || !RestoredPaused(restoredRoot) {
		t.Fatal(result, err)
	}
	var queued int
	restored.DB.QueryRow("SELECT count(*) FROM knowledge_runs WHERE state='queued'").Scan(&queued)
	if queued != 5 {
		t.Fatal(queued)
	}
}

func TestKnowledgeBatchConcurrentReplayAndFailedAttempt(t *testing.T) {
	s, w := batchFixture(t)
	token := strings.Repeat("n", 26)
	type outcome struct {
		result KnowledgeBatchResult
		err    error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() { r, e := s.QueueKnowledgeBatch(token, 5, true); results <- outcome{r, e} }()
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.result.New != 5 || b.result.New != 5 || a.result.Replayed == b.result.Replayed {
		t.Fatal(a, b)
	}
	w.Generator.(*fixtureKnowledgeGenerator).FailLinks = true
	knowledgeStep(t, w)
	summary, err := s.KnowledgeBatchSummary()
	if err != nil || summary.Continue != 1 {
		t.Fatal(summary, err)
	}
	repeated, err := s.QueueKnowledgeBatch(token, 5, true)
	if err != nil || !repeated.Replayed {
		t.Fatal(repeated, err)
	}
	after, err := s.KnowledgeBatchSummary()
	if err != nil || after.Continue != 1 || after.Active != 4 {
		t.Fatal(after, err)
	}
}

func TestKnowledgeBatchMissingFirstFileDoesNotStarveNext(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	for i := 0; i < 3; i++ {
		id := add(t, s, fmt.Sprintf("https://example.com/file-%d", i))
		manual(t, s, id, fmt.Sprintf("Distinct saved source %d", i))
		completeFixtureDigest(t, s)
	}
	s.KnowledgeEnabled = true
	var path string
	s.DB.QueryRow("SELECT text_path FROM source_versions WHERE item_id=1").Scan(&path)
	if err := os.Remove(filepath.Join(s.Files.Root, path)); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueueKnowledgeBatch(strings.Repeat("p", 26), 1, false)
	if err != nil || result.New != 1 || result.Skipped != 1 || len(result.Rows) != 2 || result.Rows[1].ItemID != 2 {
		t.Fatal(result, err)
	}
}
