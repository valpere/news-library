package library

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedFileWriteDoesNotPublishSnapshot(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/write-failure")
	os.WriteFile(filepath.Join(s.Files.Root, "texts"), []byte("blocks directory"), 0600)
	if e := s.Manual(id, "keep this"); e == nil {
		t.Fatal("failed write accepted")
	}
	i, _ := s.Item(id)
	snapshots, e := s.Texts(id)
	if e != nil || i.HasText || len(snapshots) != 0 {
		t.Fatal("failed write published metadata")
	}
}

func TestPrimaryFilesUseFourHashPairs(t *testing.T) {
	f := Files{Root: t.TempDir()}
	data := []byte("sharded primary fixture")
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	for kind, extension := range map[string]string{"texts": ".txt", "digests": ".md", "cache/http": ".body"} {
		path, err := f.Put(kind, data)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(kind, hash[:2], hash[2:4], hash[4:6], hash[6:8], hash+extension)
		if kind == "cache/http" {
			want = filepath.Join(kind, hash+extension)
		}
		if path != want {
			t.Fatalf("%s: got %s, want %s", kind, path, want)
		}
		if got, err := f.Read(path, hash); err != nil || string(got) != string(data) {
			t.Fatal("read sharded blob", err)
		}
		if again, err := f.Put(kind, data); err != nil || again != path {
			t.Fatal("dedup changed path", err)
		}
		if err := os.WriteFile(filepath.Join(f.Root, path), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Read(path, hash); err == nil {
			t.Fatal("accepted corrupt blob")
		}
		if _, err := f.Put(kind, data); kind != "cache/http" && err == nil {
			t.Fatal("overwrote corrupt immutable primary")
		}
	}
}
