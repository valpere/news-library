package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func backupStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestLiveBackupRestoresPrimaryHistoryQueueAndCheckpoints(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/backup")
	manual(t, s, id, "Persistent searchable database source")
	job := completeFixtureDigest(t, s)
	item, _ := s.Item(id)
	sid := item.Selected.Int64
	s.Rate(id, 3, &sid, nil, "unknown date")
	s.SetPlacement(id, "deferred")
	// A pending regeneration with a saved digest checkpoint must survive.
	s.RegenerateDigest(id, sid)
	pending := claim(t, s)
	if err := s.SaveDigestCheckpoint(pending, 0, 0, "input", Digest{Markdown: fixtureDigest, ActualModel: "fixture:cloud", Provider: "fixture"}); err != nil {
		t.Fatal(err)
	}
	_ = job
	root := t.TempDir()
	snapshot := filepath.Join(root, "backup")
	if _, err := Backup(context.Background(), s.Files.Root, snapshot); err != nil {
		t.Fatal(err)
	}
	// Live source changes made after the snapshot are deliberately not in it.
	s.Rate(id, 0, &sid, nil, "later judgment")
	if _, err := VerifyBackup(snapshot); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restored")
	if err := Restore(context.Background(), snapshot, target); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(target, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	item, err = restored.Item(id)
	if err != nil || item.Score.Int64 != 3 || item.Placement != "deferred" || !RestoredPaused(target) {
		t.Fatal(item, err)
	}
	history, _ := restored.Ratings(id)
	if len(history) != 1 || history[0].AssessedAt.Valid || history[0].SourceVersionID.Int64 != sid {
		t.Fatal("history changed")
	}
	source, err := restored.Snapshot(sid)
	if err != nil || source.Text != "Persistent searchable database source" {
		t.Fatal(source, err)
	}
	if digest, err := restored.DigestForSnapshot(sid); err != nil || digest == nil || digest.Markdown != fixtureDigest {
		t.Fatal(digest, err)
	}
	if got, err := restored.DigestCheckpoint(pending, 0, 0, "input"); err != nil || got == nil || got.Markdown != fixtureDigest {
		t.Fatal(got, err)
	}
	if results, err := restored.Search(Filters{Query: "searchable"}); err != nil || len(results) != 1 {
		t.Fatal(results, err)
	}
	jobs, _ := restored.Jobs(id)
	if len(jobs) != 3 || jobs[0].State != "running" {
		t.Fatal("queue lost", jobs)
	}
	if err := ResumeRestored(target); err != nil || RestoredPaused(target) {
		t.Fatal(err)
	}
}
func TestBackupRejectsCorruptionSymlinksAndOverwrite(t *testing.T) {
	s := backupStore(t)
	id := add(t, s, "https://example.com/backup-guard")
	manual(t, s, id, "keep primary source")
	root := t.TempDir()
	dest := filepath.Join(root, "copy")
	manifest, err := Backup(context.Background(), s.Files.Root, dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Backup(context.Background(), s.Files.Root, dest); err == nil {
		t.Fatal("overwrote backup")
	}
	if err = Restore(context.Background(), dest, s.Files.Root); err == nil {
		t.Fatal("overwrote active data")
	}
	if _, err = os.Stat(filepath.Join(dest, "cache")); !os.IsNotExist(err) {
		t.Fatal("cache copied")
	}
	primary := manifest.Files[0].Path
	if err = os.WriteFile(filepath.Join(dest, primary), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(dest); err == nil {
		t.Fatal("corruption not detected")
	}
	if err = Restore(context.Background(), dest, filepath.Join(root, "bad-restore")); err == nil {
		t.Fatal("corrupt restore accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "bad-restore")); !os.IsNotExist(err) {
		t.Fatal("invalid restore reserved destination")
	}
	item, _ := s.Item(id)
	source, _ := s.Snapshot(item.Selected.Int64)
	os.Remove(filepath.Join(s.Files.Root, "texts", source.Hash+".txt"))
	outside := filepath.Join(root, "outside")
	os.WriteFile(outside, []byte(source.Text), 0600)
	os.Symlink(outside, filepath.Join(s.Files.Root, "texts", source.Hash+".txt"))
	incomplete := filepath.Join(root, "incomplete")
	if _, err = Backup(context.Background(), s.Files.Root, incomplete); err == nil {
		t.Fatal("followed primary symlink")
	}
	if _, err = Open(filepath.Join(incomplete, "library.sqlite")); err == nil {
		t.Fatal("incomplete transfer opened")
	}
}

func TestLegacyBackupDoesNotMigrateOriginal(t *testing.T) {
	path := legacyDB(t)
	source := filepath.Dir(path)
	if err := os.Rename(path, filepath.Join(source, "library.sqlite")); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "legacy-backup")
	manifest, err := Backup(context.Background(), source, backup)
	if err != nil || manifest.Schema != 1 {
		t.Fatal(manifest.Schema, err)
	}
	original, err := readonlyDB(filepath.Join(source, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	var version int
	original.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 1 {
		t.Fatal("original migrated")
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err = Restore(context.Background(), backup, restored); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(filepath.Join(restored, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	item, err := copy.Item(7)
	if err != nil || item.Score.Int64 != 3 {
		t.Fatal(item, err)
	}
}
