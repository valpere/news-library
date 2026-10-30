package main

import (
	"github.com/valpere/news-library/internal/library"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveCLIAndRestorePause(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	os.Mkdir(source, 0700)
	store, err := library.Open(filepath.Join(source, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.AddURL("https://example.com/archive")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Manual(id, "Archived source"); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(root, "backup")
	restored := filepath.Join(root, "restored")
	for _, command := range []struct {
		name string
		args []string
	}{{"backup", []string{"-source", source, "-output", backup}}, {"verify-backup", []string{"-source", backup}}, {"restore", []string{"-source", backup, "-output", restored}}} {
		if err = archiveCommand(command.name, command.args); err != nil {
			t.Fatal(command.name, err)
		}
	}
	c := config{NoWorker: false}
	if err = applyRestorePause(&c, restored); err != nil || !c.NoWorker {
		t.Fatal("restored worker gate not applied", err)
	}
	c.ResumeRestored = true
	c.NoWorker = false
	if err = applyRestorePause(&c, restored); err != nil || c.NoWorker || library.RestoredPaused(restored) {
		t.Fatal(err)
	}
	if err = archiveCommand("restore", []string{"-source", backup, "-output", source}); err == nil {
		t.Fatal("CLI overwrote source")
	}
}
