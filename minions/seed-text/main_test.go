package main

import (
	"path/filepath"
	"testing"

	"github.com/valpere/news-library/internal/library"
)

func TestRunSeedsTextForTheNewItem(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, "https://example.invalid/seed", "Seeded text about backups", "", 0.8); err != nil {
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
	if err = run(dir, "https://example.invalid/seed2", "Another text", "", 0.8); err != nil {
		t.Fatal(err)
	}
}

func TestRunAppliesFilterRules(t *testing.T) {
	dir := t.TempDir()
	rules := `[{"id":"ad","reason":"commercial","field":"text","contains":"advertisement","probability":0.95}]`
	if err := run(dir, "https://example.invalid/ad", "An advertisement source", rules, 0.8); err != nil {
		t.Fatal(err)
	}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.Item(1)
	if err != nil || item.FilterState != "rejected" {
		t.Fatalf("rules not applied: %+v %v", item, err)
	}
	if err = run(t.TempDir(), "https://example.invalid/bad", "text", "not json", 0.8); err == nil {
		t.Fatal("invalid rules accepted")
	}
}

func TestRunUsesTheGivenThreshold(t *testing.T) {
	dir := t.TempDir()
	rules := `[{"id":"ad","reason":"commercial","field":"text","contains":"advertisement","probability":0.6}]`
	if err := run(dir, "https://example.invalid/ad", "An advertisement source", rules, 0.5); err != nil {
		t.Fatal(err)
	}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if item, err := store.Item(1); err != nil || item.FilterState != "rejected" {
		t.Fatalf("threshold ignored: %+v %v", item, err)
	}
}
