package library

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func failedDigestFixture(t *testing.T, s *Store, kind string) DigestRetryCandidate {
	t.Helper()
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	id := add(t, s, fmt.Sprintf("https://example.com/batch-%d", n))
	manual(t, s, id, "Persistent source text")
	j := claim(t, s)
	if j.Stage != "digest" || j.ItemID != id {
		t.Fatal(j)
	}
	if err := s.FailError(j, &SourceError{Kind: kind, Message: "<script>failure</script>", Diagnostic: "rejected reply"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	return DigestRetryCandidate{ItemID: id, SnapshotID: j.SnapshotID.Int64, JobID: j.ID, AttemptToken: j.Token}
}
func TestBatchDigestRetryPolicyAndLegacy(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	kinds := []string{"format", "temporary", "unknown", "configuration", "limit", "source"}
	for _, kind := range kinds {
		failedDigestFixture(t, s, kind)
	}
	candidates, more, err := s.FailedDigestCandidates(0)
	if err != nil || more || len(candidates) != 6 {
		t.Fatal(candidates, more, err)
	}
	for i, c := range candidates {
		if c.Kind != kinds[i] || c.Checked != (i < 2) || c.Selectable != (i < 3) {
			t.Fatal(c)
		}
	}
	s.DigestConfig.PromptVersion += "-changed"
	changed, _, err := s.FailedDigestCandidates(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changed[3:] {
		if !c.Selectable || c.Checked {
			t.Fatal(c)
		}
	}
	for _, message := range []string{"неправильна структура дайджесту", "відсутня або порожня секція дайджесту"} {
		if legacyFailureKind(message) != "format" {
			t.Fatal(message)
		}
	}
	if legacyFailureKind("дайджест порожній або перевищує ліміт") != "unknown" {
		t.Fatal("ambiguous legacy limit selected automatically")
	}
}
func TestBatchDigestRetryHistoryAndDuplicateSubmission(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	a := failedDigestFixture(t, s, "format")
	b := failedDigestFixture(t, s, "temporary")
	if err := s.Rate(a.ItemID, 3, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Item(a.ItemID)
	selected := []DigestRetryCandidate{a, b}
	if _, err := s.RetryFailedDigests([]DigestRetryCandidate{a, a}); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	results, err := s.RetryFailedDigests(selected)
	if err != nil || len(results) != 2 || !results[0].Queued || !results[1].Queued {
		t.Fatal(results, err)
	}
	repeated, err := s.RetryFailedDigests(selected)
	if err != nil || repeated[0].Queued || repeated[1].Queued {
		t.Fatal(repeated, err)
	}
	for _, c := range selected {
		history, err := s.JobFailures(c.JobID)
		if err != nil || len(history) != 1 || history[0].Response != "rejected reply" {
			t.Fatal(history, err)
		}
		var state string
		var attempts int
		if err = s.DB.QueryRow("SELECT state,attempts FROM jobs WHERE id=?", c.JobID).Scan(&state, &attempts); err != nil || state != "queued" || attempts != 0 {
			t.Fatal(state, attempts, err)
		}
	}
	after, _ := s.Item(a.ItemID)
	if after.Read != before.Read || after.Score != before.Score || after.Selected != before.Selected {
		t.Fatal(before, after)
	}
	completeFixtureDigest(t, s)
	completeFixtureDigest(t, s)
	candidates, _, err := s.FailedDigestCandidates(0)
	if err != nil || len(candidates) != 0 {
		t.Fatal(candidates, err)
	}
	ratings, _ := s.Ratings(a.ItemID)
	if len(ratings) != 1 {
		t.Fatal(ratings)
	}
}
func TestBatchDigestRetrySkipsChangedAndMissingSources(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	a := failedDigestFixture(t, s, "format")
	b := failedDigestFixture(t, s, "format")
	c := failedDigestFixture(t, s, "format")
	stale := a
	stale.JobID++
	var path string
	if err := s.DB.QueryRow("SELECT text_path FROM source_versions WHERE id=?", b.SnapshotID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.Files.Root, path)); err != nil {
		t.Fatal(err)
	}
	// All fixture texts share a file: restore it for c with a distinct source.
	manual(t, s, c.ItemID, "Distinct current source")
	results, err := s.RetryFailedDigests([]DigestRetryCandidate{stale, b, c})
	if err != nil || len(results) != 3 {
		t.Fatal(results, err)
	}
	for _, r := range results {
		if r.Queued || r.Reason == "" {
			t.Fatal(r)
		}
	}
}
func TestBatchDigestRetryWebGuardsAndUnknownOptIn(t *testing.T) {
	s, w := protectedTestWeb(t)
	enableDigests(t, s)
	a := failedDigestFixture(t, s, "format")
	failedDigestFixture(t, s, "unknown")
	page := webRequest(w, "GET", "/digest-retries", nil, nil)
	if page.Code != 200 || strings.Count(page.Body.String(), `name="selection"`) != 2 || strings.Contains(page.Body.String(), "<script>failure</script>") {
		t.Fatal(page.Code, page.Body.String())
	}
	opted := webRequest(w, "GET", "/digest-retries?include_unknown=1", nil, nil)
	if opted.Code != 200 || strings.Count(opted.Body.String(), "checked") <= strings.Count(page.Body.String(), "checked") {
		t.Fatal(opted.Body.String())
	}
	values := url.Values{"selection": {retrySelection(a)}}
	if r := webRequest(w, "POST", "/digest-retries", values, nil); r.Code != 403 {
		t.Fatal(r.Code)
	}
	values.Set("csrf", w.CSRF)
	if r := webRequest(w, "POST", "/digest-retries", values, map[string]string{"Authorization": "Bearer agent"}); r.Code != 403 {
		t.Fatal(r.Code)
	}
	values.Add("selection", "invalid")
	if r := webRequest(w, "POST", "/digest-retries", values, nil); r.Code != 400 {
		t.Fatal(r.Code)
	}
	var state string
	s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", a.JobID).Scan(&state)
	if state != "failed" {
		t.Fatal("invalid batch partially applied", state)
	}
	values["selection"] = []string{retrySelection(a)}
	if r := webRequest(w, "POST", "/digest-retries", values, nil); r.Code != 200 || !strings.Contains(r.Body.String(), "У чергу: 1") {
		t.Fatal(r.Code, r.Body.String())
	}
}

func TestBatchDigestRetryMigratedFilterAndPartialProgress(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	a := failedDigestFixture(t, s, "format")
	b := failedDigestFixture(t, s, "format")
	// Model a migrated source with no historical filter decision.
	if _, err := s.DB.Exec("DROP TRIGGER immutable_filter_delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DELETE FROM filter_decisions WHERE item_id=?", a.ItemID); err != nil {
		t.Fatal(err)
	}
	configureRules(t, s, .8, []FilterRule{{ID: "exclude", Reason: "fixture exclusion", Field: "text", Contains: "Persistent", Probability: .9}})
	results, err := s.RetryFailedDigests([]DigestRetryCandidate{a, b})
	if err != nil || results[0].Queued || !results[1].Queued {
		t.Fatal(results, err)
	}
	item, _ := s.Item(a.ItemID)
	if item.FilterState != "rejected" {
		t.Fatal("filter decision rolled back", item)
	}
	var active int
	if err = s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND stage='digest' AND state IN ('queued','running','retry_wait')", a.ItemID).Scan(&active); err != nil || active != 0 {
		t.Fatal("filtered item queued", active, err)
	}
	// Existing accepted decisions remain unchanged when rules change.
	completeFixtureDigest(t, s)
}

func TestBatchDigestRetryRejectsOldPageAfterAnotherFailedAttempt(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	old := failedDigestFixture(t, s, "format")
	if _, err := s.RetryFailedDigests([]DigestRetryCandidate{old}); err != nil {
		t.Fatal(err)
	}
	j := claim(t, s)
	if err := s.FailError(j, &SourceError{Kind: "format", Message: "another failure"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	results, err := s.RetryFailedDigests([]DigestRetryCandidate{old})
	if err != nil || results[0].Queued || !strings.Contains(results[0].Reason, "спроба змінилася") {
		t.Fatal(results, err)
	}
	current, _, err := s.FailedDigestCandidates(0)
	if err != nil || len(current) != 1 || current[0].AttemptToken == old.AttemptToken {
		t.Fatal(current, err)
	}
	results, err = s.RetryFailedDigests(current)
	if err != nil || !results[0].Queued {
		t.Fatal(results, err)
	}
}
func TestBatchDigestRetryPaginationAndExistingDigestProtection(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	seed := failedDigestFixture(t, s, "format")
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 500; i++ {
		u := fmt.Sprintf("https://example.com/pagination-%d", i)
		r, e := tx.Exec("INSERT INTO items(original_url,canonical_url,added_at,state) VALUES(?,?,?,'digest_failed')", u, u, nowUTC())
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		r, e = tx.Exec(`INSERT INTO source_versions(item_id,text_path,title,hash,retrieved_at,origin,extractor,completeness,warning) SELECT ?,text_path,title,hash,retrieved_at,origin,extractor,completeness,warning FROM source_versions WHERE id=?`, id, seed.SnapshotID)
		if e != nil {
			t.Fatal(e)
		}
		sid, _ := r.LastInsertId()
		if _, e = tx.Exec("UPDATE items SET selected_snapshot=? WHERE id=?", sid, id); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO jobs(item_id,snapshot_id,stage,state,config_version,error) VALUES(?,?,'digest','failed',?,'legacy unknown')", id, sid, s.DigestConfig.Version()); e != nil {
			t.Fatal(e)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	first, more, err := s.FailedDigestCandidates(0)
	if err != nil || len(first) != 500 || !more {
		t.Fatal(len(first), more, err)
	}
	next, more, err := s.FailedDigestCandidates(first[499].ItemID)
	if err != nil || len(next) != 1 || more || next[0].ItemID <= first[499].ItemID {
		t.Fatal(next, more, err)
	}
	// A failed later generation must not replace an already accepted digest.
	if err = s.RetryDigest(seed.ItemID); err != nil {
		t.Fatal(err)
	}
	completeFixtureDigest(t, s)
	s.DigestConfig.PromptVersion += "-new"
	if err = s.RetryDigest(seed.ItemID); err != nil {
		t.Fatal(err)
	}
	j := claim(t, s)
	if err = s.FailError(j, &SourceError{Kind: "format", Message: "bad regeneration"}, false, 3, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	results, err := s.RetryFailedDigests([]DigestRetryCandidate{{ItemID: seed.ItemID, SnapshotID: seed.SnapshotID, JobID: j.ID, AttemptToken: j.Token}})
	if err != nil || results[0].Queued {
		t.Fatal(results, err)
	}
}
