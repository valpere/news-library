package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO items(id,original_url,canonical_url,added_at,title,selected_snapshot,state) VALUES(7,'https://example.com/old','https://example.com/old','2020-01-01T00:00:00Z','Old',12,'ready'); INSERT INTO source_versions(id,item_id,text,title,hash,retrieved_at,origin,extractor,completeness,warning) VALUES(12,7,'legacy searchable text','Old','legacyhash','2020-01-01T00:00:00Z','manual','manual','unknown',''); INSERT INTO rating_events(id,item_id,score,recorded_at,assessed_at,source_version_id,digest_id,reason) VALUES(41,7,3,'2020-01-01T00:00:00Z',NULL,12,NULL,'historic'); INSERT INTO jobs(id,item_id,state) VALUES(55,7,'queued'); INSERT INTO item_tags(item_id,tag) VALUES(7,'old'); INSERT INTO item_fts(rowid,title,text,url) VALUES(7,'Old','legacy searchable text','https://example.com/old');`)
	if e != nil {
		t.Fatal(e)
	}
	return path
}
func TestExistingDatabaseMigrationAndPortableFiles(t *testing.T) {
	path := legacyDB(t)
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	snapshots, e := s.Snapshots(7)
	if e != nil || len(snapshots) != 1 || snapshots[0].ID != 12 || snapshots[0].Text != "legacy searchable text" {
		t.Fatal("migration lost text", e)
	}
	r, e := s.Ratings(7)
	if e != nil || len(r) != 1 || r[0].ID != 41 || r[0].AssessedAt.Valid || r[0].SourceVersionID.Int64 != 12 {
		t.Fatal("rating migration", r, e)
	}
	if e = s.Rate(7, 0, nil, nil, ""); e != nil {
		t.Fatal(e)
	}
	r, _ = s.Ratings(7)
	if r[0].ID <= 41 {
		t.Fatal("rating sequence reset")
	}
	rows, e := s.Search(Filters{Query: "searchable", Tags: []string{"old"}})
	if e != nil || len(rows) != 1 {
		t.Fatal("FTS migration", e)
	}
	jobs, e := s.Jobs(7)
	if e != nil || len(jobs) != 1 || jobs[0].ID != 55 || jobs[0].Stage != "fetch" {
		t.Fatal("queue migration", e)
	}
	var columnCount int
	s.DB.QueryRow(`SELECT count(*) FROM pragma_table_info('source_versions') WHERE name='text'`).Scan(&columnCount)
	if columnCount != 0 {
		t.Fatal("text remains SQL payload")
	}
	var blob string
	s.DB.QueryRow("SELECT text_path FROM source_versions WHERE id=12").Scan(&blob)
	s.Close()
	// Restore database + primary files into a different root.
	restored := t.TempDir()
	dbBytes, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(restored, "library.db"), dbBytes, 0600)
	data, e := os.ReadFile(filepath.Join(filepath.Dir(path), blob))
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Join(restored, "texts"), 0700)
	os.WriteFile(filepath.Join(restored, blob), data, 0600)
	s, e = Open(filepath.Join(restored, "library.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	snap, e := s.Snapshot(12)
	if e != nil || snap.Text != string(data) {
		t.Fatal("nonportable paths", e)
	}
	if _, e = s.DB.Exec("UPDATE source_versions SET title='changed'"); e == nil {
		t.Fatal("migration lost immutability")
	}
	os.WriteFile(filepath.Join(restored, blob), []byte("damaged"), 0600)
	if _, e = s.Snapshot(12); e == nil {
		t.Fatal("corrupt primary silently accepted")
	}
	os.Remove(filepath.Join(restored, blob))
	if _, e = s.Snapshot(12); e == nil {
		t.Fatal("missing primary silently accepted")
	}
}
func TestMigrationFailureKeepsLegacyDatabase(t *testing.T) {
	path := legacyDB(t)
	os.WriteFile(filepath.Join(filepath.Dir(path), "texts"), []byte("blocks directory"), 0600)
	if s, e := Open(path); e == nil {
		s.Close()
		t.Fatal("migration ignored failed file write")
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 1 {
		t.Fatal("failed migration published schema", version)
	}
	var text string
	if e = db.QueryRow("SELECT text FROM source_versions WHERE id=12").Scan(&text); e != nil || !strings.Contains(text, "legacy") {
		t.Fatal("failed migration lost legacy text", e)
	}
}
func TestFailedFileWriteDoesNotPublishSnapshot(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/write-failure")
	os.WriteFile(filepath.Join(s.Files.Root, "texts"), []byte("blocks directory"), 0600)
	if e := s.Manual(id, "keep this"); e == nil {
		t.Fatal("failed write accepted")
	}
	i, _ := s.Item(id)
	snapshots, e := s.Snapshots(id)
	if e != nil || i.Selected.Valid || len(snapshots) != 0 {
		t.Fatal("failed write published metadata")
	}
}

func TestV2DigestMigrationPreservesArtifactContext(t *testing.T) {
	path := legacyDB(t)
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(schemaV2); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO jobs(id,item_id,stage,state,snapshot_id,model,prompt_version,config_version) VALUES(56,7,'digest','done',12,'fixture:cloud','uk-sections-v1','fixture'); INSERT INTO generated_artifacts(id,item_id,snapshot_id,job_id,markdown,model,actual_model,provider,prompt_version,created_at) VALUES(5,7,12,56,?,'fixture:cloud','fixture:cloud','fixture','uk-sections-v1','2020-01-01T00:00:00Z'); INSERT INTO rating_events(id,item_id,score,recorded_at,source_version_id,digest_id,reason) VALUES(42,7,2,'2020-01-01T00:00:00Z',12,5,'shown digest');`, fixtureDigest)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := s.Digest(5, 7)
	if e != nil || d.Markdown != fixtureDigest || d.JobID != 56 || d.SnapshotID != 12 {
		t.Fatal("artifact migration", d, e)
	}
	r, e := s.Ratings(7)
	if e != nil || r[0].ID != 42 || r[0].DigestID.Int64 != 5 || r[0].AssessedAt.Valid {
		t.Fatal("artifact rating migration", r, e)
	}
}
