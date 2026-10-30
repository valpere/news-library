package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDigestEquivalentHeadingsAndFencedExamples(t *testing.T) {
	for _, heading := range []string{"Факти", "Цитати", "Факти та цитати", "**Факти / цитати:**", "факти/цитати"} {
		raw := strings.Replace(fixtureDigest, "## Факти / цитати", "## "+heading, 1)
		parts, err := SplitDigest(raw)
		if err != nil || len(parts) != 5 || parts[4].Title != "Факти / цитати" {
			t.Fatal(heading, parts, err)
		}
	}
	raw := strings.Replace(fixtureDigest, "Черга відновлюється після перезапуску.", "Приклад:\n    ## Інша назва в коді\n```markdown\n## Факти\nЦе текст прикладу, не секція дайджесту.\n```", 1)
	parts, err := SplitDigest(raw)
	if err != nil || len(parts) != 5 || !strings.Contains(parts[2].Markdown, "## Факти") {
		t.Fatal(parts, err)
	}
	parts, err = SplitDigest(strings.Replace(fixtureDigest, "Один сервіс.", "", 1))
	if err != nil || len(parts) != 4 {
		t.Fatal(parts, err)
	}
}
func TestDigestValidationSpecificReasonsAndLimits(t *testing.T) {
	for _, tc := range []struct{ raw, contains, kind string }{
		{strings.Replace(fixtureDigest, "Пошук прочитаних матеріалів.", "", 1), "Практичне застосування", "format"},
		{strings.Replace(fixtureDigest, "## Деталі", "## Інше", 1), "Інше", "format"},
		{fixtureDigest + strings.Repeat("x", 32<<10), "32 КіБ", "limit"},
	} {
		_, err := SplitDigest(tc.raw)
		var validation *DigestValidationError
		if !errors.As(err, &validation) || !strings.Contains(err.Error(), tc.contains) || validation.Kind != tc.kind {
			t.Fatal(tc.kind, err)
		}
		source := digestFailureError(err, tc.raw)
		if source.Kind != tc.kind || source.Retry || source.Diagnostic != tc.raw {
			t.Fatal(source)
		}
	}
}
func TestGenerationFailureKindsHistoryAndBackup(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/failure")
	manual(t, s, id, "A saved source survives malformed model replies.")
	calls := 0
	worker := &Worker{Store: s, Generator: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
		calls++
		return Digest{}, &SourceError{Kind: "format", Message: "Секція порожня", Diagnostic: "<script>untrusted()</script> raw model reply"}
	}), Timeout: time.Second, Lease: time.Second, MaxAttempts: 3}
	if worked, err := worker.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	jobs, _ := s.Jobs(id)
	job := jobs[0]
	events, err := s.JobFailures(job.ID)
	if err != nil || len(events) != 1 || events[0].Kind != "format" || events[0].Retryable || events[0].Response == "" {
		t.Fatal(events, err)
	}
	if worked, err := worker.Step(context.Background()); err != nil || worked || calls != 1 {
		t.Fatal("format failure auto-repeated", worked, err, calls)
	}
	if err = s.RetryDigest(id); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err = s.JobFailures(job.ID)
	if err != nil || len(events) != 2 || events[1].Response == "" {
		t.Fatal("failure history lost on explicit retry", events, err)
	}
	if e := s.FailError(Job{ID: job.ID, Token: "wrong", Attempts: 1}, &SourceError{Kind: "format", Diagnostic: "stale reply", Message: "stale"}, false, 3, time.Now(), 0); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("stale lease published failure", e)
	}
	preserved, _ := s.JobFailures(job.ID)
	if len(preserved) != 2 {
		t.Fatal(preserved)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	restoredDir := filepath.Join(root, "restored")
	if _, err = Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err = Restore(context.Background(), backup, restoredDir); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(restoredDir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	old, err := restored.JobFailures(job.ID)
	if err != nil || len(old) != 2 || old[0].Response != events[0].Response {
		t.Fatal(old, err)
	}
	var path string
	s.DB.QueryRow("SELECT response_path FROM job_failures ORDER BY id DESC LIMIT 1").Scan(&path)
	if err = os.WriteFile(filepath.Join(s.Files.Root, path), []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	events, err = s.JobFailures(job.ID)
	if err != nil || events[0].DiagnosticError == "" {
		t.Fatal("diagnostic error hid metadata", events, err)
	}
	item, _ := s.Item(id)
	if !item.Selected.Valid || item.Score.Valid || item.Read {
		t.Fatal(item)
	}
}
func TestGenerationFailureAdviceAndTemporaryRetry(t *testing.T) {
	for _, kind := range []string{"temporary", "format", "configuration", "limit", "source", "unknown"} {
		label, advice, button := FailureAdvice(kind)
		if label == "" || advice == "" || button == "" {
			t.Fatal(kind)
		}
	}
	s := backupStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/temporary")
	manual(t, s, id, "Persisted source")
	calls := 0
	worker := &Worker{Store: s, Generator: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
		calls++
		return Digest{}, &SourceError{Kind: "temporary", Message: "Ollama HTTP 429", Retry: true}
	}), Timeout: time.Second, Lease: time.Second, MaxAttempts: 3}
	if _, err := worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	item, _ := s.Item(id)
	if item.State != "digest_retry_wait" {
		t.Fatal(item)
	}
	if _, err := worker.Step(context.Background()); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	jobs, _ := s.Jobs(id)
	events, err := s.JobFailures(jobs[0].ID)
	if err != nil || len(events) != 2 || !events[0].Retryable || events[0].Kind != "temporary" {
		t.Fatal(events, err)
	}
}

func TestDigestFailureUIAndNonRetryableCategories(t *testing.T) {
	for _, kind := range []string{"format", "configuration", "limit"} {
		t.Run(kind, func(t *testing.T) {
			s := backupStore(t)
			enableDigests(t, s)
			id := add(t, s, "https://example.com/"+kind)
			manual(t, s, id, "Primary source remains readable")
			worker := &Worker{Store: s, Generator: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
				return Digest{}, &SourceError{Kind: kind, Message: "Fixture refusal", Diagnostic: "<script>untrusted()</script>", Retry: true}
			}), Timeout: time.Second, Lease: time.Second, MaxAttempts: 3}
			if _, err := worker.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			item, _ := s.Item(id)
			if item.State != "digest_failed" {
				t.Fatal("permanent category auto-retried", item)
			}
			w, err := NewWeb(s, "http://127.0.0.1:8787", 4096)
			if err != nil {
				t.Fatal(err)
			}
			response := webRequest(w, "GET", fmt.Sprintf("/items/%d", id), nil, nil)
			_, _, button := FailureAdvice(kind)
			if response.Code != 200 || !strings.Contains(response.Body.String(), `data-kind="`+kind+`"`) || !strings.Contains(response.Body.String(), button) || !strings.Contains(response.Body.String(), "Primary source remains readable") || strings.Contains(response.Body.String(), "<script>untrusted()</script>") {
				t.Fatal(response.Code, response.Body.String())
			}
			var path string
			s.DB.QueryRow("SELECT response_path FROM job_failures ORDER BY id DESC LIMIT 1").Scan(&path)
			os.Remove(filepath.Join(s.Files.Root, path))
			response = webRequest(w, "GET", fmt.Sprintf("/items/%d", id), nil, nil)
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Primary source remains readable") || !strings.Contains(response.Body.String(), "діагностична відповідь недоступна") {
				t.Fatal("diagnostic loss hid primary content", response.Code, response.Body.String())
			}
		})
	}
}
