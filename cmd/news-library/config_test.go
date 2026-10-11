package main

import (
	"io"
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

func TestM3ConfigPrecedenceAndSecretErrors(t *testing.T) {
	token := strings.Repeat("t", 32)
	c, err := readConfig([]string{"-env-file", "", "-embeddings", "-embed-url", "http://127.0.0.1:11434/api/embed", "-read-token", token})
	if err != nil || !c.Embeddings || c.ReadToken != token {
		t.Fatal(c.Embeddings, err)
	}
	t.Setenv("NEWS_LIBRARY_EMBEDDINGS", "bad-private-value")
	if _, err = readConfig([]string{"-env-file", ""}); err == nil || strings.Contains(err.Error(), "bad-private-value") {
		t.Fatal(err)
	}
	c, err = readConfig([]string{"-env-file", "", "-embeddings=false"})
	if err != nil || c.Embeddings {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-read-token", "short-private-token"}, {"-embed-timeout", "0s"}, {"-embeddings", "-embed-url", "http://external.example/api/embed"}} {
		if _, err = readConfig(append([]string{"-env-file", "", "-embeddings=false"}, args...)); err == nil || strings.Contains(err.Error(), "short-private-token") {
			t.Fatal(err)
		}
	}
}

func TestHelpDoesNotExposeReadToken(t *testing.T) {
	token := strings.Repeat("private-fixture-", 3)
	t.Setenv("NEWS_LIBRARY_READ_TOKEN", token)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = old; reader.Close(); writer.Close() }()
	_, err = readConfig([]string{"-env-file", "", "-h"})
	writer.Close()
	os.Stderr = old
	output, readErr := io.ReadAll(reader)
	if readErr != nil || err == nil || strings.Contains(string(output), token) || !strings.Contains(string(output), "read-token") {
		t.Fatal("CLI help exposed a credential or was unavailable")
	}
}

func TestTelegramConfigPrecedenceAndPrivateErrors(t *testing.T) {
	token := "123:fixture-secret-token-1234567890"
	t.Setenv("NEWS_LIBRARY_TELEGRAM_BOT_TOKEN", token)
	t.Setenv("NEWS_LIBRARY_TELEGRAM_CHAT_ID", "42")
	c, err := readConfig([]string{"-env-file", "", "-telegram", "-telegram-daily", "09:00", "-telegram-size", "5"})
	if err != nil || !c.Telegram.Enabled || c.Telegram.DailyTime != "09:00" || c.Telegram.Size != 5 {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-telegram-daily", "25:99"}, {"-telegram-size", "0"}, {"-telegram-chat", "invalid-private-chat"}} {
		if _, err = readConfig(append([]string{"-env-file", "", "-telegram"}, args...)); err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "invalid-private-chat") {
			t.Fatal(err)
		}
	}
}

func TestYouTubeConfigPrecedence(t *testing.T) {
	t.Setenv("NEWS_LIBRARY_YT_DLP", "fixture-helper")
	t.Setenv("NEWS_LIBRARY_YOUTUBE_LANGUAGES", "uk,en")
	c, err := readConfig([]string{"-env-file", ""})
	if err != nil || c.YTDLP != "fixture-helper" || c.YouTubeLanguages != "uk,en" {
		t.Fatal(c.YTDLP, c.YouTubeLanguages, err)
	}
	c, err = readConfig([]string{"-env-file", "", "-yt-dlp=", "-youtube-languages", "en,uk"})
	if err != nil || c.YTDLP != "" || c.YouTubeLanguages != "en,uk" {
		t.Fatal(c.YTDLP, c.YouTubeLanguages, err)
	}
	if _, err = readConfig([]string{"-env-file", "", "-youtube-languages", "en;bad"}); err == nil {
		t.Fatal("invalid language codes accepted")
	}
}
