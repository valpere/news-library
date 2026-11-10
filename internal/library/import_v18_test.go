package library

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// oldV18DDL is the part of the v18 schema the importer reads.
const oldV18DDL = `
CREATE TABLE items (id INTEGER PRIMARY KEY, original_url TEXT NOT NULL, canonical_url TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL,
 placement TEXT NOT NULL DEFAULT 'inbox', is_read INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '', selected_snapshot INTEGER,
 filter_state TEXT NOT NULL DEFAULT 'not_checked');
CREATE TABLE source_aliases (url TEXT PRIMARY KEY, item_id INTEGER NOT NULL);
CREATE TABLE source_versions (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, job_id INTEGER, title TEXT NOT NULL, hash TEXT NOT NULL, retrieved_at TEXT NOT NULL,
 origin TEXT NOT NULL, extractor TEXT NOT NULL, completeness TEXT NOT NULL, warning TEXT NOT NULL, text_path TEXT NOT NULL DEFAULT '');
CREATE TABLE item_tags (item_id INTEGER NOT NULL, tag TEXT NOT NULL, origin TEXT NOT NULL DEFAULT 'human', PRIMARY KEY(item_id,tag,origin));
CREATE TABLE generated_artifacts (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, snapshot_id INTEGER NOT NULL, job_id INTEGER NOT NULL UNIQUE,
 model TEXT NOT NULL, actual_model TEXT NOT NULL, provider TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL,
 input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, markdown_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '', chunk_count INTEGER NOT NULL DEFAULT 1);
CREATE TABLE rating_events (id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL, score INTEGER NOT NULL, recorded_at TEXT NOT NULL, assessed_at TEXT,
 source_version_id INTEGER, digest_id INTEGER, reason TEXT NOT NULL, scale TEXT NOT NULL DEFAULT 'legacy_numeric');
CREATE TABLE filter_decisions (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, snapshot_id INTEGER, rules_version TEXT NOT NULL, stage TEXT NOT NULL, result TEXT NOT NULL,
 probability REAL NOT NULL, threshold REAL NOT NULL, contributions TEXT NOT NULL, created_at TEXT NOT NULL, override_of INTEGER);
CREATE TABLE digest_feedback (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, digest_id INTEGER NOT NULL, quality TEXT NOT NULL, note TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE TABLE interest_predictions (digest_id INTEGER PRIMARY KEY, probability REAL NOT NULL, reason TEXT NOT NULL, profile TEXT NOT NULL, profile_hash TEXT NOT NULL);
CREATE TABLE completeness_confirmations (id INTEGER PRIMARY KEY, snapshot_id INTEGER NOT NULL, complete INTEGER NOT NULL, note TEXT NOT NULL DEFAULT '', recorded_at TEXT NOT NULL);
CREATE TABLE trash_entries (item_id INTEGER PRIMARY KEY, reason TEXT NOT NULL, moved_at TEXT NOT NULL);
CREATE TABLE search_events (id INTEGER PRIMARY KEY, query TEXT NOT NULL, mode TEXT NOT NULL, actual_mode TEXT NOT NULL, filters TEXT NOT NULL, result_ids TEXT NOT NULL,
 created_at TEXT NOT NULL, not_found_at TEXT);
CREATE TABLE search_judgments (id INTEGER PRIMARY KEY, event_id INTEGER NOT NULL, item_id INTEGER NOT NULL, score INTEGER NOT NULL, recorded_at TEXT NOT NULL);
PRAGMA user_version=18;
`

type oldFixture struct {
	root string
	db   *sql.DB
	t    *testing.T
}

func newOldFixture(t *testing.T) *oldFixture {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(oldV18DDL); err != nil {
		t.Fatal(err)
	}
	return &oldFixture{root: root, db: db, t: t}
}
func (f *oldFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

// file stores data under the old sharded layout and returns the relative path and hash.
func (f *oldFixture) file(kind, ext, data string) (string, string) {
	f.t.Helper()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
	rel := filepath.Join(kind, hash[:2], hash[2:4], hash+ext)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, rel)), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(data), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return rel, hash
}
func (f *oldFixture) item(id int, url, state string, selected any) {
	f.exec(`INSERT INTO items(id,original_url,canonical_url,title,added_at,placement,is_read,state,selected_snapshot,filter_state) VALUES(?,?,?,?,?,'library',1,?,?,'passed')`,
		id, url, url, "T"+url, "2026-01-0"+fmt.Sprint(id)+"T00:00:00Z", state, selected)
}
func (f *oldFixture) snapshot(id, item int, text string) {
	rel, hash := f.file("texts", ".txt", text)
	f.exec(`INSERT INTO source_versions(id,item_id,title,hash,retrieved_at,origin,extractor,completeness,warning,text_path) VALUES(?,?,?,?,?,'fetch','x-v1','complete','',?)`,
		id, item, "S"+fmt.Sprint(id), hash, "2026-02-01T00:00:00Z", rel)
}
func (f *oldFixture) digest(id, item, snapshot int, md string) {
	rel, hash := f.file("digests", ".md", md)
	f.exec(`INSERT INTO generated_artifacts(id,item_id,snapshot_id,job_id,model,actual_model,provider,prompt_version,created_at,input_tokens,output_tokens,markdown_path,hash,chunk_count) VALUES(?,?,?,?,'m','am','p','pv','2026-03-01T00:00:00Z',5,6,?,?,2)`,
		id, item, snapshot, id, rel, hash)
}

func importFixture(t *testing.T, f *oldFixture) (*Store, ImportReport) {
	t.Helper()
	newRoot := t.TempDir()
	report, err := ImportV18(f.root, newRoot)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(newRoot, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, report
}
func scalar[T any](t *testing.T, s *Store, q string, args ...any) T {
	t.Helper()
	var v T
	if err := s.DB.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestImportV18CarriesUserDataAndCurrentTextAndDigest(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", 11)
	f.snapshot(10, 1, "old text")
	f.snapshot(11, 1, "current text")
	f.digest(20, 1, 10, "# old digest")
	f.digest(21, 1, 11, "# first digest of current text")
	f.digest(22, 1, 11, "# latest digest of current text")
	f.exec(`INSERT INTO rating_events(item_id,score,recorded_at,assessed_at,reason,scale) VALUES(1,2,'2026-04-01T00:00:00Z',NULL,'','estimative_usefulness_v1'),(1,5,'2026-04-02T00:00:00Z','2026-04-01T12:00:00Z','','estimative_usefulness_v1')`)
	f.exec(`INSERT INTO item_tags VALUES(1,'go','human'),(1,'ai','model')`)
	f.exec(`INSERT INTO source_aliases VALUES('https://alias.example/1',1)`)
	f.exec(`INSERT INTO digest_feedback(item_id,digest_id,quality,note,recorded_at) VALUES(1,21,'unclear','n','2026-05-01T00:00:00Z'),(1,22,'good','n','2026-05-02T00:00:00Z')`)
	f.exec(`INSERT INTO interest_predictions VALUES(22,0.7,'r','profile','h')`)
	f.exec(`INSERT INTO completeness_confirmations(snapshot_id,complete,recorded_at) VALUES(11,0,'2026-05-01T00:00:00Z'),(11,1,'2026-05-02T00:00:00Z')`)
	f.exec(`INSERT INTO search_events VALUES(1,'q','fts','fts','{}','[1]','2026-06-01T00:00:00Z',NULL)`)
	f.exec(`INSERT INTO search_judgments VALUES(1,1,1,2,'2026-06-02T00:00:00Z')`)

	s, report := importFixture(t, f)

	if n := scalar[int](t, s, `SELECT count(*) FROM items`); n != 1 {
		t.Fatalf("items %d", n)
	}
	var rating int
	var ratedAt, added string
	var read int
	if err := s.DB.QueryRow(`SELECT rating,rated_at,added_at,is_read FROM items WHERE id=1`).Scan(&rating, &ratedAt, &added, &read); err != nil {
		t.Fatal(err)
	}
	if rating != 5 || ratedAt != "2026-04-01T12:00:00Z" || added != "2026-01-01T00:00:00Z" || read != 1 {
		t.Fatalf("item %d %s %s %d", rating, ratedAt, added, read)
	}
	if got, err := s.SourceText(1); err != nil || got.Text != "current text" {
		t.Fatalf("text %q %v", got.Text, err)
	}
	d, err := s.DigestOf(1)
	if err != nil || d == nil || d.Markdown != "# latest digest of current text" || d.ChunkCount != 2 || d.Feedback != "good" {
		t.Fatalf("digest %+v %v", d, err)
	}
	if scalar[int](t, s, `SELECT count(*) FROM digests`) != 1 || scalar[int](t, s, `SELECT count(*) FROM texts`) != 1 {
		t.Fatal("only the current text and digest are kept")
	}
	if scalar[int](t, s, `SELECT complete_confirmed FROM texts WHERE item_id=1`) != 1 {
		t.Fatal("latest completeness confirmation is carried")
	}
	if scalar[float64](t, s, `SELECT probability FROM interest_predictions WHERE item_id=1`) != 0.7 {
		t.Fatal("prediction follows the kept digest")
	}
	if scalar[int](t, s, `SELECT count(*) FROM item_tags`) != 2 || scalar[int](t, s, `SELECT count(*) FROM source_aliases`) != 1 {
		t.Fatal("tags and aliases")
	}
	if scalar[int](t, s, `SELECT count(*) FROM search_events`) != 1 || scalar[int](t, s, `SELECT count(*) FROM search_judgments WHERE score=2`) != 1 {
		t.Fatal("search judgments")
	}
	if scalar[int](t, s, `SELECT count(*) FROM item_fts WHERE item_fts MATCH 'current'`) != 1 {
		t.Fatal("current text is indexed")
	}
	if scalar[int](t, s, `SELECT count(*) FROM jobs`) != 0 {
		t.Fatal("a fully imported item needs no job")
	}
	if report.Items != 1 || report.Texts != 1 || report.Digests != 1 {
		t.Fatalf("report %+v", report)
	}
}

func TestImportV18TrashItemWithoutTextAndLegacyRating(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", 10)
	f.snapshot(10, 1, "text")
	f.exec(`INSERT INTO trash_entries VALUES(1,'junk','2026-07-01T00:00:00Z')`)
	f.exec(`INSERT INTO rating_events(item_id,score,recorded_at,reason,scale) VALUES(1,9,'2026-04-01T00:00:00Z','','legacy_numeric')`)
	f.item(2, "https://a.example/2", "needs_input", nil)
	f.item(3, "https://a.example/3", "queued", nil)

	s, report := importFixture(t, f)

	if scalar[string](t, s, `SELECT moved_at FROM trash_entries WHERE item_id=1`) != "2026-07-01T00:00:00Z" {
		t.Fatal("trash entry")
	}
	if scalar[int](t, s, `SELECT count(*) FROM item_fts WHERE rowid=1`) != 0 {
		t.Fatal("trashed item is not indexed")
	}
	if scalar[int](t, s, `SELECT count(*) FROM items WHERE rating IS NOT NULL`) != 0 {
		t.Fatal("legacy numeric ratings are not human usefulness ratings")
	}
	if report.SkippedLegacyRatings != 1 {
		t.Fatalf("report %+v", report)
	}
	if scalar[string](t, s, `SELECT state FROM items WHERE id=2`) != "needs_input" || scalar[int](t, s, `SELECT count(*) FROM jobs WHERE item_id=2`) != 0 {
		t.Fatal("needs_input stays as is")
	}
	if scalar[string](t, s, `SELECT state FROM items WHERE id=3`) != "queued" || scalar[int](t, s, `SELECT count(*) FROM jobs WHERE item_id=3 AND stage='fetch' AND state='queued'`) != 1 {
		t.Fatal("an item with no text is queued for fetching")
	}
}

func TestImportV18FallsBackToLatestSnapshotAndRejectsCorruptFile(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", nil)
	f.snapshot(10, 1, "first")
	f.snapshot(12, 1, "second")
	s, _ := importFixture(t, f)
	if got, err := s.SourceText(1); err != nil || got.Text != "second" {
		t.Fatalf("text %q %v", got.Text, err)
	}

	bad := newOldFixture(t)
	bad.item(1, "https://a.example/1", "ready", 10)
	bad.snapshot(10, 1, "text")
	var rel string
	if err := bad.db.QueryRow(`SELECT text_path FROM source_versions`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad.root, rel), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	if _, err := ImportV18(bad.root, newRoot); err == nil {
		t.Fatal("a corrupt source file must fail the import")
	}
	if _, err := os.Stat(filepath.Join(newRoot, "library.sqlite")); err == nil {
		t.Fatal("a failed import leaves no database behind")
	}
}

func TestImportV18RefusesNonEmptyTargetAndWrongSchema(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", nil)
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "library.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportV18(f.root, occupied); err == nil {
		t.Fatal("target with a database must be refused")
	}
	f.exec(`PRAGMA user_version=17`)
	if _, err := ImportV18(f.root, t.TempDir()); err == nil {
		t.Fatal("only schema 18 is imported")
	}
}

func TestImportV18DoesNotModifySource(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", 10)
	f.snapshot(10, 1, "text")
	before, err := os.ReadFile(filepath.Join(f.root, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	importFixture(t, f)
	after, err := os.ReadFile(filepath.Join(f.root, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the source database changed")
	}
}

func TestImportV18AcceptsRelativePaths(t *testing.T) {
	f := newOldFixture(t)
	f.item(1, "https://a.example/1", "ready", 10)
	f.snapshot(10, 1, "text")
	base := t.TempDir()
	t.Chdir(base)
	if err := os.Rename(f.root, filepath.Join(base, "old")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportV18("old", "new"); err != nil {
		t.Fatal(err)
	}
}
