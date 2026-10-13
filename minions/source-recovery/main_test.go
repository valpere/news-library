package main

import (
	"flag"
	"github.com/valpere/news-library/internal/library"
	"os"
	"path/filepath"
	"testing"
)

func TestOfflinePlanLeavesQueueAndHumanDataUntouched(t *testing.T) {
	root := t.TempDir()
	s, err := library.Open(filepath.Join(root, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.AddURL("https://example.com/unread")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddURL("https://example.com/trash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO trash_entries(item_id,reason,moved_at) VALUES(?,'fixture','unknown')", second); err != nil {
		t.Fatal(err)
	}
	ids, err := plan(s, "", 30)
	if err != nil || len(ids) != 1 || ids[0] != first {
		t.Fatal(ids, err)
	}
	before, err := humanState(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	os.Args = []string{"source-recovery", "-data", root}
	flag.CommandLine = flag.NewFlagSet("fixture", flag.ContinueOnError)
	if err = run(); err != nil {
		t.Fatal(err)
	}
	after, err := humanState(s.DB)
	if err != nil || before != after {
		t.Fatal("human data changed", err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM jobs WHERE state='queued'").Scan(&count); err != nil || count != 2 {
		t.Fatal("queue changed", count, err)
	}
	ids, err = plan(s, "1,1,2", 30)
	if err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if _, err = plan(s, "1,2", 1); err == nil {
		t.Fatal("batch limit ignored")
	}
}

func TestGenerationRequiresExplicitApply(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	root := filepath.Join(t.TempDir(), "missing")
	os.Args = []string{"source-recovery", "-data", root, "-run-cloud"}
	flag.CommandLine = flag.NewFlagSet("fixture", flag.ContinueOnError)
	if err := run(); err == nil {
		t.Fatal("generation accepted without apply")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unexpected data directory creation", err)
	}
}
