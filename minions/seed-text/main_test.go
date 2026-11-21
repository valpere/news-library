package main

import (
	"path/filepath"
	"testing"

	"github.com/valpere/news-library/internal/library"
)

func TestRunSeedsTextForTheNewItem(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, "https://example.invalid/seed", "Seeded text about backups"); err != nil {
		t.Fatal(err)
	}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.Item(1)
	if err != nil || !item.HasText || item.State != "ready" {
		t.Fatalf("item not seeded: %+v %v", item, err)
	}
	// A second seed works on an empty queue too.
	if err = run(dir, "https://example.invalid/seed2", "Another text"); err != nil {
		t.Fatal(err)
	}
}
