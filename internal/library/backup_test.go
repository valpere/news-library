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
	s.Rate(id, 3, nil)
	// A pending regeneration with a saved digest checkpoint must survive.
	s.RegenerateDigest(id)
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
	s.Rate(id, 0, nil)
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
	if err != nil || item.Score.Int64 != 3 || !RestoredPaused(target) {
		t.Fatal(item, err)
	}
	history, _ := restored.Ratings(id)
	if len(history) != 1 || history[0].AssessedAt.Valid {
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
	var sourcePath string
	s.DB.QueryRow("SELECT text_path FROM source_versions WHERE id=?", source.ID).Scan(&sourcePath)
	os.Remove(filepath.Join(s.Files.Root, sourcePath))
	outside := filepath.Join(root, "outside")
	os.WriteFile(outside, []byte(source.Text), 0600)
	os.Symlink(outside, filepath.Join(s.Files.Root, sourcePath))
	incomplete := filepath.Join(root, "incomplete")
	if _, err = Backup(context.Background(), s.Files.Root, incomplete); err == nil {
		t.Fatal("followed primary symlink")
	}
	if _, err = Open(filepath.Join(incomplete, "library.sqlite")); err == nil {
		t.Fatal("incomplete transfer opened")
	}
	// A symlink at any newly introduced shard directory is also rejected.
	if err := os.Remove(filepath.Join(s.Files.Root, sourcePath)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Files.Root, sourcePath), []byte(source.Text), 0600); err != nil {
		t.Fatal(err)
	}
	shard := filepath.Join(s.Files.Root, "texts", source.Hash[:2])
	moved := filepath.Join(root, "moved-shard")
	if err := os.Rename(shard, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, shard); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), s.Files.Root, filepath.Join(root, "symlink-shard")); err == nil {
		t.Fatal("followed shard directory symlink")
	}
}

func mixedLayoutStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s := backupStore(t)
	enableDigests(t, s)
	old := add(t, s, "https://example.com/flat")
	manual(t, s, old, "historical flat source")
	completeFixtureDigest(t, s)
	// Reproduce a pre-sharding store without depending on the new writer.
	for _, ref := range []struct{ table, column, kind, extension, trigger string }{
		{"source_versions", "text_path", "texts", ".txt", "immutable_snapshot_update"},
		{"generated_artifacts", "markdown_path", "digests", ".md", "immutable_artifact_update"},
	} {
		var path, hash, triggerSQL string
		if err := s.DB.QueryRow("SELECT "+ref.column+",hash FROM "+ref.table+" WHERE item_id=?", old).Scan(&path, &hash); err != nil {
			t.Fatal(err)
		}
		flat := filepath.Join(ref.kind, hash+ref.extension)
		if err := os.Rename(filepath.Join(s.Files.Root, path), filepath.Join(s.Files.Root, flat)); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow("SELECT sql FROM sqlite_master WHERE name=?", ref.trigger).Scan(&triggerSQL); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("DROP TRIGGER " + ref.trigger); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("UPDATE "+ref.table+" SET "+ref.column+"=? WHERE item_id=?", flat, old); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(triggerSQL); err != nil {
			t.Fatal(err)
		}
	}
	fresh := add(t, s, "https://example.com/sharded")
	manual(t, s, fresh, "new sharded source")
	completeFixtureDigest(t, s)
	return s, old, fresh
}

// A *_path column the inventory forgets would silently drop files from backups.
func TestBackupInventoryCoversEveryStoredPathColumn(t *testing.T) {
	s := backupStore(t)
	rows, err := s.DB.Query(`SELECT m.name,p.name FROM sqlite_master m, pragma_table_info(m.name) p
 WHERE m.type='table' AND p.name LIKE '%\_path' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	listed := map[[2]string]bool{}
	for _, ref := range primaryTables() {
		listed[[2]string{ref.table, ref.column}] = true
	}
	for rows.Next() {
		var table, column string
		if err = rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		// The HTTP cache is derived and restorable; everything else is primary.
		if table != "cache_entries" && !listed[[2]string{table, column}] {
			t.Errorf("%s.%s is not in the backup inventory", table, column)
		}
	}
}
