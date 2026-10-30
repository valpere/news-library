package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/valpere/news-library/internal/library"
)

func invoke(t *testing.T, args ...string) error {
	t.Helper()
	oldFlags, oldArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = oldFlags, oldArgs }()
	flag.CommandLine = flag.NewFlagSet("recovery", flag.ContinueOnError)
	os.Args = append([]string{"recovery"}, args...)
	return run()
}
func TestDefaultAuditDoesNotModifyDatabaseOrGenerate(t *testing.T) {
	root := t.TempDir()
	s, err := library.Open(filepath.Join(root, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.AddURL("https://example.com/read-only-audit")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rate(id, 5, nil); err != nil {
		t.Fatal(err)
	}
	before, err := ratingsHash(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "audit.json")
	if err := invoke(t, "-data", root, "-output", report); err != nil {
		t.Fatal(err)
	}
	after, err := ratingsHash(s.DB)
	if err != nil || before != after {
		t.Fatal("audit changed human ratings", err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM knowledge_batches").Scan(&count); err != nil || count != 0 {
		t.Fatal("audit queued work", err)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatal(err)
	}
	if err := invoke(t, "-data", root, "-run-cloud"); err == nil {
		t.Fatal("cloud enabled without apply")
	}
	if err := os.WriteFile(filepath.Join(root, ".restore-paused"), []byte("paused"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := invoke(t, "-data", root, "-apply"); err == nil {
		t.Fatal("bypassed restored pause")
	}
}
