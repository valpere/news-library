package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChangedTextErasesEarlierTextAndItsDerivedData(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/replace")
	manual(t, s, id, "First text of the article.")
	completeFixtureDigest(t, s)
	old, _ := s.Item(id)
	var textPath, digestPath string
	s.DB.QueryRow("SELECT text_path FROM source_versions WHERE id=?", old.Selected.Int64).Scan(&textPath)
	s.DB.QueryRow("SELECT markdown_path FROM generated_artifacts WHERE snapshot_id=?", old.Selected.Int64).Scan(&digestPath)
	if textPath == "" || digestPath == "" {
		t.Fatal("fixture files missing")
	}
	if err := s.Rate(id, 4, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCompleteness(old.Selected.Int64, true, ""); err != nil {
		t.Fatal(err)
	}
	s.SetRead(id, true)
	s.SetTags(id, []string{"mine"})

	manual(t, s, id, "A different second text.")

	item, _ := s.Item(id)
	if item.Score.Valid || !item.Read || item.Tags != "mine" || item.State != "enriching" {
		t.Fatal("rating must be erased, read and tags kept, post-processing queued", item)
	}
	if n := count(t, s, "SELECT count(*) FROM source_versions WHERE item_id=?", id); n != 1 {
		t.Fatal("source rows", n)
	}
	if n := count(t, s, "SELECT count(*) FROM generated_artifacts WHERE item_id=?", id); n != 0 {
		t.Fatal("digest of the replaced text kept", n)
	}
	if n := count(t, s, "SELECT count(*) FROM completeness_confirmations WHERE snapshot_id=?", old.Selected.Int64); n != 0 {
		t.Fatal("confirmation kept", n)
	}
	if _, err := os.Stat(filepath.Join(s.Files.Root, textPath)); !os.IsNotExist(err) {
		t.Fatal("file of the replaced text kept", err)
	}
	if _, err := os.Stat(filepath.Join(s.Files.Root, digestPath)); !os.IsNotExist(err) {
		t.Fatal("file of the replaced digest kept", err)
	}
}

func TestSameTextChangesNothing(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/same")
	manual(t, s, id, "The same text.")
	completeFixtureDigest(t, s)
	before, _ := s.Item(id)
	if err := s.Rate(id, 5, nil); err != nil {
		t.Fatal(err)
	}
	manual(t, s, id, "The same text.")
	after, _ := s.Item(id)
	if after.Selected != before.Selected || !after.Score.Valid || after.Score.Int64 != 5 || after.State != "ready" {
		t.Fatal("identical text invalidated something", before, after)
	}
	if n := count(t, s, "SELECT count(*) FROM source_versions WHERE item_id=?", id); n != 1 {
		t.Fatal("identical text added a row", n)
	}
}

func TestRefetchWithChangedTextReplacesTheOldOne(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/refetch")
	first := claim(t, s)
	snap := Snapshot{Title: "T", Text: "The original article text, long enough to count as a source.", Origin: "fetch", Extractor: "fixture", Completeness: "full"}
	if err := s.Complete(first, snap); err != nil {
		t.Fatal(err)
	}
	s.Rate(id, 2, nil)
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	same := claim(t, s)
	if err := s.Complete(same, snap); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Ratings(id); len(r) != 1 {
		t.Fatal("same refetched text erased the rating")
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	changed := claim(t, s)
	snap.Text = "The article was edited by its author and now says something else."
	if err := s.Complete(changed, snap); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Ratings(id); len(r) != 0 {
		t.Fatal("changed refetched text kept the rating")
	}
	if vs, _ := s.Snapshots(id); len(vs) != 1 || vs[0].Text != snap.Text {
		t.Fatal("old text kept", vs)
	}
}
