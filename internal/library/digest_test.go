package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixtureDigest = "## Про що матеріал\nСтислий огляд збереження новин.\n## Ключові тези\n- Текст залишається доступним.\n## Деталі\nЧерга відновлюється після перезапуску.\n## Практичне застосування\nПошук прочитаних матеріалів.\n## Факти / цитати\nОдин сервіс."

func enableDigests(t *testing.T, s *Store) {
	t.Helper()
	if e := s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 4096}); e != nil {
		t.Fatal(e)
	}
}
func completeFixtureDigest(t *testing.T, s *Store) Job {
	t.Helper()
	j := claim(t, s)
	if j.Stage != "digest" {
		t.Fatal("not digest", j)
	}
	if e := s.CompleteDigest(j, Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture"}); e != nil {
		t.Fatal(e)
	}
	return j
}
func TestDigestQueueRecoveryAndRatingContext(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/digest")
	manual(t, s, id, "Persistent source text")
	j := claim(t, s)
	if j.Stage != "digest" {
		t.Fatal(j)
	}
	// Simulate a restart: inactive generation leaves persisted leases untouched.
	s.DB.Exec("UPDATE jobs SET attempts=3,lease_until=0 WHERE id=?", j.ID)
	s.DigestConfig = nil
	if next, e := s.Claim(context.Background(), time.Now(), time.Minute, 3); e != nil || next != nil {
		t.Fatal(next, e)
	}
	jobs, _ := s.Jobs(id)
	if jobs[0].State != "running" {
		t.Fatal("disabled generation consumed lease")
	}
	enableDigests(t, s)
	if next, e := s.Claim(context.Background(), time.Now(), time.Minute, 3); e != nil || next != nil {
		t.Fatal(next, e)
	}
	i, _ := s.Item(id)
	if i.State != "digest_failed" {
		t.Fatal("expired generation not diagnosed", i)
	}
	if e := s.RetryDigest(id); e != nil {
		t.Fatal(e)
	}
	next := claim(t, s)
	if e := s.CompleteDigest(j, Digest{Markdown: fixtureDigest}); e == nil {
		t.Fatal("stale lease committed")
	}
	if e := s.CompleteDigest(next, Digest{Markdown: fixtureDigest, ActualModel: next.Model, Provider: "fixture"}); e != nil {
		t.Fatal(e)
	}
	d, e := s.DigestForSnapshot(next.SnapshotID.Int64)
	if e != nil || d == nil {
		t.Fatal(e)
	}
	if e = s.Rate(id, 3, nil); e != nil {
		t.Fatal(e)
	}
	manual(t, s, id, "New source text")
	completeFixtureDigest(t, s)
	if r, _ := s.Ratings(id); len(r) != 0 {
		t.Fatal("rating survived a text change", r)
	}
	other := add(t, s, "https://example.com/other")
	manual(t, s, other, "Other text")
	completeFixtureDigest(t, s)
	// A digest job of a replaced text is gone: finishing it is refused.
	manual(t, s, id, "Third text")
	older := claim(t, s)
	manual(t, s, id, "Fourth text")
	if e = s.CompleteDigest(older, Digest{Markdown: fixtureDigest}); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("digest of a replaced text accepted", e)
	}
	i, _ = s.Item(id)
	if i.State != "enriching" {
		t.Fatal("old completion overwrote manual text")
	}
}
func TestDigestPrimaryUIAndExports(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/digest")
	manual(t, s, id, "Full source <script>alert(1)</script>")
	rw := webRequest(w, "GET", "/items/1", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), `data-pending="true"`) || strings.Contains(rw.Body.String(), `action="/items/1/rating"`) {
		t.Fatal("premature assessment", rw.Body.String())
	}
	completeFixtureDigest(t, s)
	rw = webRequest(w, "GET", "/items/1", nil, nil)
	html := rw.Body.String()
	if rw.Code != 200 || !strings.Contains(html, `id="digest"`) || !strings.Contains(html, `name="digest"`) || strings.Contains(html, `data-pending="true"`) || strings.Contains(html, "<script>alert") {
		t.Fatal("digest UI", html)
	}
	snapshots, _ := s.Snapshots(id)
	d, _ := s.DigestForSnapshot(snapshots[0].ID)
	post(t, w, "/items/1/rating", url.Values{"score": {"3"}, "source": {strconv.FormatInt(d.SnapshotID, 10)}, "digest": {strconv.FormatInt(d.ID, 10)}})
	ratings, _ := s.Ratings(id)
	if len(ratings) != 1 || ratings[0].Score != 3 {
		t.Fatal("rating not recorded")
	}
	for _, format := range []string{"markdown", "html"} {
		rw = webRequest(w, "GET", fmt.Sprintf("/items/1/digests/%d/%s", d.ID, format), nil, nil)
		if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Ключові тези") || !strings.Contains(rw.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("export", format, rw.Code)
		}
	}
	rw = webRequest(w, "GET", fmt.Sprintf("/items/2/digests/%d/html", d.ID), nil, nil)
	if rw.Code != 404 {
		t.Fatal("foreign export")
	}
}
func TestOllamaCloudOnlyAndOutputValidation(t *testing.T) {
	chatCalls := 0
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tags" {
			host := "https://ollama.com"
			if mode == "local" {
				host = ""
			}
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "fixture:cloud", "remote_model": "fixture:31b", "remote_host": host}, {"name": "fixture:31b"}}})
			return
		}
		chatCalls++
		var payload struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Model != "fixture:cloud" {
			t.Error("local model used", payload.Model)
		}
		markdown := fixtureDigest
		done := true
		if mode == "invalid" {
			markdown = "unsupported answer"
		}
		if mode == "partial" {
			done = false
		}
		json.NewEncoder(w).Encode(map[string]any{"done": done, "message": map[string]string{"content": markdown}})
	}))
	defer server.Close()
	o, e := NewOllama(server.URL+"/api/chat", "", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	j := Job{Model: "fixture:31b-cloud", PromptVersion: DigestPromptVersion, InputLimit: 4096}
	snap := Snapshot{Text: "Saved source", Completeness: "partial"}
	d, e := o.Generate(context.Background(), j, snap)
	if e != nil || d.ActualModel != "fixture:cloud" || chatCalls != 1 {
		t.Fatal(d, e)
	}
	for _, m := range []string{"invalid", "partial"} {
		mode = m
		if _, e = o.Generate(context.Background(), j, snap); e == nil {
			t.Fatal("invalid output accepted", m)
		}
	}
	before := chatCalls
	mode = "local"
	if _, e = o.Generate(context.Background(), j, snap); e == nil {
		t.Fatal("unverified cloud accepted")
	}
	j.Model = "fixture:31b"
	if _, e = o.Generate(context.Background(), j, snap); e == nil {
		t.Fatal("local accepted")
	}
	j.Model = "fixture:cloud"
	snap.Text = strings.Repeat("x", 4097)
	if _, e = o.Generate(context.Background(), j, snap); e == nil {
		t.Fatal("oversized text accepted")
	}
	if chatCalls != before {
		t.Fatal("forbidden generation called")
	}
	sections, e := SplitDigest(strings.Replace(fixtureDigest, "Один сервіс.", "<script>alert(1)</script> [bad](javascript:alert(1))", 1))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(sections[4].HTML), "<script>") || strings.Contains(string(sections[4].HTML), `href="javascript:`) {
		t.Fatal("unsafe Markdown")
	}
}

type generatorFunc func(context.Context, Job, Snapshot) (Digest, error)

func (f generatorFunc) Generate(ctx context.Context, j Job, s Snapshot) (Digest, error) {
	return f(ctx, j, s)
}
func TestDigestWorkerRestartAndFailurePreservesSource(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/restart-digest")
	manual(t, s, id, "Offline text after generation failure")
	path := filepath.Join(s.Files.Root, "library.db")
	s.Close()
	restarted, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	enableDigests(t, restarted)
	calls := 0
	worker := Worker{Store: restarted, Generator: generatorFunc(func(_ context.Context, j Job, s Snapshot) (Digest, error) {
		calls++
		return Digest{}, &SourceError{Message: "temporary cloud failure", Retry: true}
	}), Timeout: time.Second, DigestTimeout: time.Second, Lease: time.Minute, Backoff: time.Hour, MaxAttempts: 3}
	if worked, e := worker.Step(context.Background()); !worked || e != nil {
		t.Fatal("worker", worked, e)
	}
	i, _ := restarted.Item(id)
	if i.State != "digest_retry_wait" {
		t.Fatal("no digest retry", i)
	}
	snapshots, e := restarted.Snapshots(id)
	if e != nil || snapshots[0].Text != "Offline text after generation failure" {
		t.Fatal("lost source", e)
	}
	if worked, e := worker.Step(context.Background()); worked || e != nil || calls != 1 {
		t.Fatal("ignored retry delay", worked, e, calls)
	}
	restarted.DB.Exec("UPDATE jobs SET next_attempt=0 WHERE stage='digest'")
	worker.Generator = generatorFunc(func(_ context.Context, j Job, s Snapshot) (Digest, error) {
		calls++
		return Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture"}, nil
	})
	if worked, e := worker.Step(context.Background()); !worked || e != nil || calls != 2 {
		t.Fatal("worker retry", worked, e, calls)
	}
	i, _ = restarted.Item(id)
	if i.State != "ready" || i.Read || i.Score.Valid {
		t.Fatal("generation mutated human decision")
	}
}
