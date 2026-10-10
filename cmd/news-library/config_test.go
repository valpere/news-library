package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrecedenceAndPrivateErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("NEWS_LIBRARY_BIND=127.0.0.1:8789\nOLLAMA_MODEL=fixture:cloud\nOLLAMA_API_KEY=private-fixture\n"), 0600)
	c, e := readConfig([]string{"--env-file=" + path})
	if e != nil || c.Bind != "127.0.0.1:8789" || c.Model != "fixture:cloud" || c.OllamaKey != "private-fixture" {
		t.Fatal("file config", e)
	}
	t.Setenv("NEWS_LIBRARY_BIND", "127.0.0.1:8790")
	c, e = readConfig([]string{"-env-file", path})
	if e != nil || c.Bind != "127.0.0.1:8790" {
		t.Fatal("environment precedence", e)
	}
	c, e = readConfig([]string{"-env-file", path, "-bind", "127.0.0.1:8791"})
	if e != nil || c.Bind != "127.0.0.1:8791" {
		t.Fatal("flag precedence", e)
	}
	t.Setenv("NEWS_LIBRARY_ATTEMPTS", "private-fixture")
	_, e = readConfig([]string{"-env-file", path})
	if e == nil || strings.Contains(e.Error(), "private-fixture") {
		t.Fatal("invalid setting leaked value", e)
	}
	c, e = readConfig([]string{"-env-file", path, "-attempts", "3"})
	if e != nil || c.Attempts != 3 {
		t.Fatal("invalid lower priority defeated valid flag", e)
	}
	if _, e = readConfig([]string{"-env-file", filepath.Join(t.TempDir(), "missing")}); e == nil {
		t.Fatal("explicit missing config silently ignored")
	}
}
