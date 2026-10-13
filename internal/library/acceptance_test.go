package library

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// AT-10/15/16: recover the missing-source path without fabricating content or
// changing the user's judgment, including after a generation failure/restart.
func TestRecoveryPreservesManualSourceAndHumanJudgment(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	raw := "https://example.com/manual-recovery"
	id := add(t, s, raw)
	fetchCalls, generationCalls := 0, 0
	w := &Worker{Store: s, Source: sourceFunc(func(context.Context, string) (Snapshot, error) {
		fetchCalls++
		return Snapshot{}, &SourceError{Kind: "source", Message: "fixture subtitles unavailable"}
	}), Generator: generatorFunc(func(_ context.Context, j Job, snap Snapshot) (Digest, error) {
		generationCalls++
		if snap.Text != "User supplied source" {
			t.Fatal("wrong source", snap.Text)
		}
		if generationCalls == 1 {
			return Digest{}, &SourceError{Kind: "temporary", Message: "fixture cloud unavailable", Retry: true}
		}
		return Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture"}, nil
	}), Timeout: time.Second, DigestTimeout: time.Second, Lease: time.Minute, Backoff: time.Hour, MaxAttempts: 3}
	if worked, err := w.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	item, err := s.Item(id)
	if err != nil || item.State != "needs_input" || item.URL != raw || item.Selected.Valid || item.Score.Valid {
		t.Fatal(item, err)
	}
	var artifacts int
	if err = s.DB.QueryRow("SELECT count(*) FROM generated_artifacts").Scan(&artifacts); err != nil || artifacts != 0 || generationCalls != 0 || fetchCalls != 1 {
		t.Fatal("missing text generated a digest", artifacts, generationCalls, fetchCalls, err)
	}
	manual(t, s, id, "User supplied source")
	item, err = s.Item(id)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := item.Selected.Int64
	snap, err := s.Snapshot(sourceID)
	if err != nil || snap.Origin != "manual" || snap.Extractor != "manual-v1" || snap.Completeness != "unknown" {
		t.Fatal("manual provenance", snap, err)
	}
	if err = s.RateUsefulnessShown(id, 3, &sourceID, nil, nil, "human judgment with unknown assessment date"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRead(id, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetTags(id, []string{"human-tag"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Ratings(id)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Files.Root, "library.db")
	reopen := func() {
		t.Helper()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s = next
		enableDigests(t, s)
		w.Store = s
	}
	reopen()
	if worked, err := w.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	item, err = s.Item(id)
	if err != nil || item.State != "digest_retry_wait" {
		t.Fatal(item, err)
	}
	reopen()
	defer s.Close()
	if worked, err := w.Step(context.Background()); err != nil || worked || generationCalls != 1 {
		t.Fatal("backoff lost at restart", worked, err, generationCalls)
	}
	if _, err = s.DB.Exec("UPDATE jobs SET next_attempt=0 WHERE item_id=? AND stage='digest'", id); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	item, err = s.Item(id)
	if err != nil || item.State != "ready" || !item.Read || item.Tags != "human-tag" || item.Selected.Int64 != sourceID || fetchCalls != 1 || generationCalls != 2 {
		t.Fatal("recovery changed human state or repeated source", item, err, fetchCalls, generationCalls)
	}
	d, err := s.DigestForSnapshot(sourceID)
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	// A later source must not rewrite the prior assessment or fill its null digest/date.
	manual(t, s, id, "Newer source version")
	after, err := s.Ratings(id)
	if err != nil || !reflect.DeepEqual(before, after) || len(after) != 1 || after[0].DigestID.Valid || after[0].AssessedAt.Valid {
		t.Fatal("judgment reinterpreted or rebound", before, after, err)
	}
	original, err := s.Snapshot(sourceID)
	if err != nil || original.Text != "User supplied source" || original.Origin != "manual" {
		t.Fatal("manual source replaced", original, err)
	}
}
