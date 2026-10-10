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

func TestM2ConfigAndRuleValidation(t *testing.T) {
	t.Setenv("NEWS_LIBRARY_INTERESTS", "Fixture profile")
	t.Setenv("NEWS_LIBRARY_FILTER_THRESHOLD", "0.8")
	t.Setenv("NEWS_LIBRARY_FILTER_RULES", `[{"id":"test","reason":"test","field":"text","contains":"fixture","probability":0.9}]`)
	c, e := readConfig([]string{"-env-file", ""})
	if e != nil || c.Interests != "Fixture profile" || c.FilterThreshold != .8 {
		t.Fatal("M2 config", e)
	}
	t.Setenv("NEWS_LIBRARY_FILTER_RULES", "private-fixture-invalid")
	if _, e = readConfig([]string{"-env-file", ""}); e == nil || strings.Contains(e.Error(), "private-fixture-invalid") {
		t.Fatal("rule error leaked configuration", e)
	}
	c, e = readConfig([]string{"-env-file", "", "-filter-rules", "[]", "-interests", ""})
	if e != nil || c.Interests != "" {
		t.Fatal("M2 flag override", e)
	}
}

func TestChunkAndCacheLimits(t *testing.T) {
	for _, args := range [][]string{{"-chunk-max-parts", "1"}, {"-cache-max-bytes", "0"}, {"-cache-retention", "-1h"}, {"-cache-cleanup-interval", "0s"}} {
		if _, e := readConfig(append([]string{"-env-file", ""}, args...)); e == nil {
			t.Fatal("invalid maintenance limit accepted", args)
		}
	}
}
