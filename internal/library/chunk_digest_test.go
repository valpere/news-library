package library

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSplitTextCoversUTF8Exactly(t *testing.T) {
	source := strings.Repeat("Український абзац із фактами.\n\n", 200)
	parts, e := SplitText(source, 1024)
	if e != nil || len(parts) < 2 || strings.Join(parts, "") != source {
		t.Fatal("source bytes lost", e)
	}
	for _, part := range parts {
		if len(part) > 1024 || !utf8.ValidString(part) {
			t.Fatal("bad chunk boundary")
		}
	}
}
func TestLongDigestResumesSavedPartsAndPreservesSource(t *testing.T) {
	s := testStore(t)
	if e := s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 1024, ChunkLimit: 1024, ChunkMaxParts: 64}); e != nil {
		t.Fatal(e)
	}
	id := add(t, s, "https://example.com/long")
	source := strings.Repeat("довгий збережений матеріал ", 120)
	manual(t, s, id, source)
	calls := 0
	failed := false
	leaf := generatorFunc(func(_ context.Context, j Job, snapshot Snapshot) (Digest, error) {
		calls++
		if !failed && calls == 2 {
			failed = true
			return Digest{}, &SourceError{Message: "temporary cloud failure", Retry: true}
		}
		return Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture", InputTokens: 10, OutputTokens: 5}, nil
	})
	g := &ChunkGenerator{Store: s, Leaf: leaf}
	worker := Worker{Store: s, Generator: g, Timeout: time.Minute, DigestTimeout: time.Minute, Lease: time.Minute, Backoff: time.Second, MaxAttempts: 3}
	if worked, e := worker.Step(context.Background()); !worked || e != nil {
		t.Fatal(e)
	}
	var checkpoints int
	s.DB.QueryRow("SELECT count(*) FROM digest_checkpoints").Scan(&checkpoints)
	if checkpoints != 1 {
		t.Fatal("no durable part")
	}
	snapshots, e := s.Snapshots(id)
	if e != nil || snapshots[0].Text != source {
		t.Fatal("full source lost", e)
	}
	path := filepath.Join(s.Files.Root, "library.db")
	s.Close()
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	s = reopened
	g.Store = s
	worker.Store = s
	if e = s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 2048, ChunkLimit: 2048, ChunkMaxParts: 64}); e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("UPDATE jobs SET next_attempt=0 WHERE stage='digest'")
	if worked, e := worker.Step(context.Background()); !worked || e != nil {
		t.Fatal(e)
	}
	d, e := s.DigestForSnapshot(snapshots[0].ID)
	if e != nil || d == nil || d.ChunkCount < 2 {
		t.Fatal("no multi-part digest", e)
	}
	s.DB.QueryRow("SELECT count(*) FROM digest_checkpoints").Scan(&checkpoints)
	if checkpoints != 0 {
		t.Fatal("checkpoints of the finished job kept", checkpoints)
	}
	// Every successful call is counted in the usage once (parts are reused, not repeated); one call failed.
	if calls != int(d.InputTokens/10)+1 {
		t.Fatal("parts repeated or usage lost", calls, d.InputTokens)
	}
}
func TestLongDigestBoundsAndFencing(t *testing.T) {
	s := testStore(t)
	s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 1024, ChunkLimit: 1024, ChunkMaxParts: 2})
	id := add(t, s, "https://example.com/too-long")
	manual(t, s, id, strings.Repeat("x", 4000))
	j := claim(t, s)
	calls := 0
	g := &ChunkGenerator{Store: s, Leaf: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) { calls++; return Digest{}, nil })}
	snap, _ := s.Snapshot(j.SnapshotID.Int64)
	if _, e := g.Generate(context.Background(), j, snap); e == nil || calls != 0 {
		t.Fatal("unbounded calls")
	}
	s.DB.Exec("UPDATE jobs SET lease_until=0 WHERE id=?", j.ID)
	if e := s.SaveDigestCheckpoint(j, 0, 0, "hash", Digest{Markdown: fixtureDigest}); e != ErrLeaseLost {
		t.Fatal("stale worker checkpoint accepted", e)
	}
	snapshots, e := s.Snapshots(id)
	if e != nil || len(snapshots[0].Text) != 4000 {
		t.Fatal("bounded failure lost source")
	}
}

func TestLongDigestPredictsInterestOnlyAtFinalSynthesis(t *testing.T) {
	s := testStore(t)
	profile := "Fixture interests"
	s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPredictionPromptVersion, Interests: profile, MaxInputBytes: 2048, ChunkLimit: 2048, ChunkMaxParts: 64})
	id := add(t, s, "https://example.com/long-predicted")
	manual(t, s, id, strings.Repeat("Source data. ", 800))
	j := claim(t, s)
	snap, _ := s.Snapshot(j.SnapshotID.Int64)
	forecasts := 0
	g := ChunkGenerator{Store: s, Leaf: generatorFunc(func(_ context.Context, part Job, s Snapshot) (Digest, error) {
		d := Digest{Markdown: fixtureDigest, ActualModel: part.Model, Provider: "fixture"}
		if part.Interests != "" {
			forecasts++
			if part.PromptVersion != DigestPredictionPromptVersion {
				t.Error("wrong synthesis prompt")
			}
			d.Prediction = &InterestPrediction{Probability: .7, Reason: "Related to the fixture profile", Profile: part.Interests}
		} else if part.PromptVersion != DigestPromptVersion {
			t.Error("part forecast was requested")
		}
		return d, nil
	})}
	d, e := g.Generate(context.Background(), j, snap)
	if e != nil || forecasts != 1 || d.Prediction == nil {
		t.Fatal("prediction not restricted to synthesis", forecasts, e)
	}
	if e = s.CompleteDigest(j, d); e != nil {
		t.Fatal(e)
	}
}
