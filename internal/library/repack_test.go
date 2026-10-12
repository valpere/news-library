package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepackFilesPreservesHistoryAndBackup(t *testing.T) {
	s, old, fresh := mixedLayoutStore(t)
	item, err := s.Item(old)
	if err != nil {
		t.Fatal(err)
	}
	sid := item.Selected.Int64
	if err := s.Rate(old, 3, &sid, nil, "historical judgment"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	manifest, err := Backup(context.Background(), s.Files.Root, backup)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "repacked")
	if err := RepackFiles(context.Background(), backup, target); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(backup); err != nil {
		t.Fatal("source backup changed", err)
	}
	if !RestoredPaused(target) {
		t.Fatal("repack resumed workers")
	}
	for _, file := range manifest.Files {
		if filepath.Dir(file.Path) == "texts" || filepath.Dir(file.Path) == "digests" {
			if _, err := os.Lstat(filepath.Join(target, file.Path)); !os.IsNotExist(err) {
				t.Fatal("flat copy left in new root", file.Path, err)
			}
			if _, err := os.Stat(filepath.Join(s.Files.Root, file.Path)); err != nil {
				t.Fatal("original primary changed", err)
			}
		}
	}
	repacked, err := Open(filepath.Join(target, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repacked.Close()
	for _, id := range []int64{old, fresh} {
		before, _ := s.Item(id)
		after, err := repacked.Item(id)
		if err != nil || before.Selected != after.Selected {
			t.Fatal("source ID changed", err)
		}
		source, err := repacked.Snapshot(after.Selected.Int64)
		if err != nil || source.Text == "" {
			t.Fatal("lost repacked source", err)
		}
		if digest, err := repacked.DigestForSnapshot(source.ID); err != nil || digest == nil || digest.Markdown != fixtureDigest {
			t.Fatal("lost repacked digest", err)
		}
	}
	ratings, err := repacked.Ratings(old)
	if err != nil || len(ratings) != 1 || ratings[0].Score != 3 || ratings[0].AssessedAt.Valid || ratings[0].SourceVersionID.Int64 != sid {
		t.Fatal("changed rating history", ratings, err)
	}
	for _, ref := range primaryTables(CurrentSchema) {
		if ref.trigger == "" {
			continue
		}
		// Even empty tables must retain their immutability guards.
		if _, err := repacked.DB.Exec("UPDATE " + ref.table + " SET " + ref.column + "=" + ref.column); ref.table == "source_versions" || ref.table == "generated_artifacts" {
			if err == nil {
				t.Fatal("lost immutable trigger", ref.table)
			}
		}
		var count int
		if err := repacked.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?", ref.trigger).Scan(&count); err != nil || count != 1 {
			t.Fatal("trigger missing", ref.trigger, err)
		}
	}
	refs, err := primaryRefs(repacked.DB, CurrentSchema)
	if err != nil {
		t.Fatal(err)
	}
	for path := range refs {
		if filepath.Dir(path) == "texts" || filepath.Dir(path) == "digests" {
			t.Fatal("flat DB reference left", path)
		}
	}
	// Repacking an already sharded copy is valid and portable too.
	secondBackup := filepath.Join(root, "second-backup")
	if _, err := Backup(context.Background(), target, secondBackup); err != nil {
		t.Fatal(err)
	}
	if err := RepackFiles(context.Background(), secondBackup, filepath.Join(root, "second-repack")); err != nil {
		t.Fatal(err)
	}
	if err := RepackFiles(context.Background(), backup, target); err == nil {
		t.Fatal("overwrote destination")
	}
	if err := RepackFiles(context.Background(), backup, filepath.Join(backup, "nested")); err == nil {
		t.Fatal("wrote inside source backup")
	}
	alias := filepath.Join(root, "backup-alias")
	if err := os.Symlink(backup, alias); err != nil {
		t.Fatal(err)
	}
	if err := RepackFiles(context.Background(), backup, filepath.Join(alias, "nested")); err == nil {
		t.Fatal("wrote inside aliased source backup")
	}
	if err := RepackFiles(context.Background(), s.Files.Root, filepath.Join(root, "not-backup")); err == nil {
		t.Fatal("accepted live data without verified backup")
	}
}

func TestRepackFailureKeepsOutputBlocked(t *testing.T) {
	s, _, _ := mixedLayoutStore(t)
	// Backup integrity permits SQLite schema differences, but the migration
	// must fail closed if an expected immutable trigger is missing.
	if _, err := s.DB.Exec("DROP TRIGGER immutable_artifact_update"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	if _, err := Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "incomplete")
	if err := RepackFiles(context.Background(), backup, target); err == nil {
		t.Fatal("ignored missing trigger")
	}
	if _, err := os.Stat(filepath.Join(target, transferMarker)); err != nil {
		t.Fatal("failed output unblocked", err)
	}
	if db, err := Open(filepath.Join(target, "library.sqlite")); err == nil {
		db.Close()
		t.Fatal("opened incomplete repack")
	}
	copied, err := readonlyDB(filepath.Join(target, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var path string
	if err := copied.QueryRow("SELECT text_path FROM source_versions ORDER BY id LIMIT 1").Scan(&path); err != nil || filepath.Dir(path) != "texts" {
		t.Fatal("rollback published new paths", path, err)
	}
	if _, err := VerifyBackup(backup); err != nil {
		t.Fatal("failure modified backup", err)
	}
}

func TestRepackIncludesEveryPrimaryTable(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	first := knowledgeArticle(t, s, "first")
	knowledgeStep(t, w)
	second := knowledgeArticle(t, s, "second")
	g.FailLinks = true
	knowledgeStep(t, w)
	if _, err := s.SaveConnection(ConnectionNote{Title: "Related sources", Body: "Shared context", Members: []ConnectionMember{{ItemID: first, Role: "First"}, {ItemID: second, Role: "Second"}}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewDelivery(deliveryConfig(), "manual", ""); err != nil {
		t.Fatal(err)
	}
	item, _ := s.Item(first)
	if err := s.RegenerateDigest(first, item.Selected.Int64); err != nil {
		t.Fatal(err)
	}
	job := claim(t, s)
	if err := s.SaveDigestCheckpoint(job, 0, 0, "fixture", Digest{Markdown: fixtureDigest}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.recordFailure(tx, job, JobFailure{Kind: "format", Message: "fixture diagnostic", Response: "rejected response"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	refs, err := primaryRefs(s.DB, CurrentSchema)
	if err != nil {
		t.Fatal(err)
	}
	moves := map[string]string{}
	for path, hash := range refs {
		kind := strings.Split(path, string(filepath.Separator))[0]
		extension := ".md"
		if kind == "texts" {
			extension = ".txt"
		}
		flat := filepath.Join(kind, hash+extension)
		if err := os.Rename(filepath.Join(s.Files.Root, path), filepath.Join(s.Files.Root, flat)); err != nil {
			t.Fatal(err)
		}
		moves[path] = flat
	}
	for _, ref := range primaryTables(CurrentSchema) {
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM " + ref.table).Scan(&count); err != nil || count == 0 {
			t.Fatal("fixture missing primary table", ref.table, err)
		}
		var guard string
		if ref.trigger != "" {
			if err := s.DB.QueryRow("SELECT sql FROM sqlite_master WHERE name=?", ref.trigger).Scan(&guard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec("DROP TRIGGER " + ref.trigger); err != nil {
				t.Fatal(err)
			}
		}
		for path, flat := range moves {
			if _, err := s.DB.Exec("UPDATE "+ref.table+" SET "+ref.column+"=? WHERE "+ref.column+"=?", flat, path); err != nil {
				t.Fatal(err)
			}
		}
		if guard != "" {
			if _, err := s.DB.Exec(guard); err != nil {
				t.Fatal(err)
			}
		}
	}
	root := t.TempDir()
	backup, target := filepath.Join(root, "backup"), filepath.Join(root, "repacked")
	if _, err := Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err := RepackFiles(context.Background(), backup, target); err != nil {
		t.Fatal(err)
	}
	repacked, err := Open(filepath.Join(target, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repacked.Close()
	got, err := primaryRefs(repacked.DB, CurrentSchema)
	if err != nil || len(got) != len(refs) {
		t.Fatal("lost primary references", err)
	}
	for path, hash := range got {
		if len(strings.Split(path, string(filepath.Separator))) != 6 {
			t.Fatal("reference not sharded", path)
		}
		if _, err := repacked.Files.Read(path, hash); err != nil {
			t.Fatal("missing repacked blob", err)
		}
	}
	if _, err := Backup(context.Background(), target, filepath.Join(root, "verified-result")); err != nil {
		t.Fatal("repacked store not backup-compatible", err)
	}
}
