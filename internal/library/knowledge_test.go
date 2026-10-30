package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureKnowledgeGenerator struct {
	MetadataCalls, LinkCalls int
	FailLinks                bool
	Invalid                  bool
	Vocabulary               map[string][]string
}

func (g *fixtureKnowledgeGenerator) Metadata(ctx context.Context, model string, c KnowledgeContext, v map[string][]string) (ArticleKnowledge, error) {
	g.MetadataCalls++
	g.Vocabulary = v
	return ArticleKnowledge{Category: "Зберігання даних", Tags: []string{"sqlite", "backup"}, Questions: []string{"Як відновити узгоджену копію?"}}, nil
}
func (g *fixtureKnowledgeGenerator) Links(ctx context.Context, model string, c KnowledgeContext, others []KnowledgeContext) ([]KnowledgeLink, error) {
	g.LinkCalls++
	if g.FailLinks {
		return nil, &SourceError{Kind: "format", Message: "invalid links", Diagnostic: "raw rejected analysis"}
	}
	other := others[0]
	l := KnowledgeLink{ItemID: other.ItemID, Title: "Узгоджене відновлення", Body: "Обидва матеріали пояснюють надійне збереження тексту. Перший розглядає чергу, другий — відновлення.", Category: "Зберігання даних", Tags: []string{"sqlite", "backup"}, Questions: []string{"Як відновити узгоджену копію?"}, CurrentRole: "Черга", OtherRole: "Відновлення", CurrentEvidence: "Текст залишається доступним.", OtherEvidence: "Текст залишається доступним."}
	if g.Invalid {
		l.ItemID = 999
		l.OtherEvidence = "invented evidence"
	}
	return []KnowledgeLink{l}, nil
}
func knowledgeArticle(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id := add(t, s, "https://example.com/"+name)
	manual(t, s, id, "SQLite backup and consistent snapshots keep persistent sources.")
	completeFixtureDigest(t, s)
	return id
}
func knowledgeStep(t *testing.T, w *KnowledgeWorker) {
	t.Helper()
	worked, err := w.Step(context.Background())
	if err != nil || !worked {
		t.Fatal(worked, err)
	}
}
func TestAutomaticKnowledgePipelineBacklinksAndHumanHistory(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	generator := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: generator, Timeout: time.Second}
	first := knowledgeArticle(t, s, "first")
	knowledgeStep(t, w)
	if generator.MetadataCalls != 1 || generator.LinkCalls != 0 {
		t.Fatal(generator)
	}
	item, _ := s.Item(first)
	source, _ := s.Snapshot(item.Selected.Int64)
	if err := s.SetTags(first, []string{"human-only"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rate(first, 3, nil); err != nil {
		t.Fatal(err)
	}
	second := knowledgeArticle(t, s, "second")
	knowledgeStep(t, w)
	if generator.MetadataCalls != 2 || generator.LinkCalls != 1 || len(generator.Vocabulary["tags"]) == 0 {
		t.Fatal(generator)
	}
	for _, id := range []int64{first, second} {
		item, _ := s.Item(id)
		k, err := s.Knowledge(id, item.Selected.Int64)
		if err != nil || k == nil || len(k.Questions) != 1 || !strings.Contains(k.Markdown, "aliases:") || !strings.Contains(k.Markdown, "url:") {
			t.Fatal(k, err)
		}
		links, err := s.Connections("", id, 0)
		if err != nil || len(links) != 1 {
			t.Fatal(links, err)
		}
		note, err := s.Connection(links[0].ID, 0)
		if err != nil || note.Author != "model" || note.Model != "fixture:cloud" || !strings.Contains(note.Body, "Підстава зв’язку") {
			t.Fatal(note, err)
		}
	}
	after, _ := s.Item(first)
	if after.Tags != "human-only" || after.Score.Int64 != 3 || after.Read {
		t.Fatal(after)
	}
	again, _ := s.Snapshot(source.ID)
	if again.Text != source.Text || again.Hash != source.Hash {
		t.Fatal("article rewritten")
	}
	results, err := s.Search(Filters{Query: "Як відновити узгоджену копію?"})
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	results, err = s.Search(Filters{Tags: []string{"sqlite"}})
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	// Re-running completed processing does not create duplicate notes or cloud calls.
	if err = s.RetryKnowledge(second); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.Step(context.Background()); err != nil || worked {
		t.Fatal(worked, err)
	}
}
func TestKnowledgeCheckpointFailureRetryAndBackup(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	first := knowledgeArticle(t, s, "first")
	knowledgeStep(t, w)
	second := knowledgeArticle(t, s, "second")
	g.FailLinks = true
	knowledgeStep(t, w)
	item, _ := s.Item(second)
	k, err := s.Knowledge(second, item.Selected.Int64)
	if err != nil || k == nil {
		t.Fatal(k, err)
	}
	status, _ := s.KnowledgeStatus(second, item.Selected.Int64)
	if status.State != "failed" || status.Kind != "format" {
		t.Fatal(status)
	}
	if worked, err := w.Step(context.Background()); err != nil || worked {
		t.Fatal("format automatically retried", worked, err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	restoredRoot := filepath.Join(root, "restored")
	if _, err = Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err = Restore(context.Background(), backup, restoredRoot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(restoredRoot, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.KnowledgeEnabled = true
	enableDigests(t, restored)
	restoredK, err := restored.Knowledge(second, item.Selected.Int64)
	if err != nil || restoredK.Markdown != k.Markdown || !RestoredPaused(restoredRoot) {
		t.Fatal(restoredK, err)
	}
	var diagnostic, hash string
	restored.DB.QueryRow("SELECT response_path,hash FROM knowledge_failures").Scan(&diagnostic, &hash)
	raw, err := restored.Files.Read(diagnostic, hash)
	if err != nil || string(raw) != "raw rejected analysis" {
		t.Fatal(string(raw), err)
	}
	g.FailLinks = false
	w.Store = restored
	if err = restored.RetryKnowledge(second); err != nil {
		t.Fatal(err)
	}
	knowledgeStep(t, w)
	if g.MetadataCalls != 2 || g.LinkCalls != 2 {
		t.Fatal("metadata checkpoint regenerated", g)
	}
	backlinks, err := restored.Connections("", first, 0)
	if err != nil || len(backlinks) != 1 {
		t.Fatal(backlinks, err)
	}
	var events int
	restored.DB.QueryRow("SELECT count(*) FROM knowledge_failures").Scan(&events)
	if events != 1 {
		t.Fatal(events)
	}
}
func TestKnowledgeStaleLeaseFilterAndInvalidEvidence(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	first := knowledgeArticle(t, s, "first")
	knowledgeStep(t, w)
	second := knowledgeArticle(t, s, "second")
	j, err := s.claimKnowledge(time.Second)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	c, err := s.knowledgeContext(second, j.SnapshotID, j.DigestID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := g.Metadata(context.Background(), j.Model, c, nil)
	digest, _ := s.Digest(j.DigestID, j.ItemID)
	if err = s.completeKnowledgeMetadata(*j, metadata, digest); err != nil {
		t.Fatal(err)
	}
	c, err = s.knowledgeContext(second, j.SnapshotID, j.DigestID)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := s.knowledgeCandidates(c)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	g.Invalid = true
	links, _ := g.Links(context.Background(), j.Model, c, candidates)
	if err = s.completeKnowledgeLinks(*j, c, candidates, links); err == nil {
		t.Fatal("invalid target accepted")
	}
	g.Invalid = false
	links, _ = g.Links(context.Background(), j.Model, c, candidates)
	links[0].OtherEvidence = "a fabricated source quote"
	if err = s.completeKnowledgeLinks(*j, c, candidates, links); err == nil {
		t.Fatal("invented quote accepted")
	}
	links, _ = g.Links(context.Background(), j.Model, c, candidates)
	stale := *j
	stale.Token = "stale"
	if err = s.completeKnowledgeLinks(stale, c, candidates, links); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	manual(t, s, second, "New selected version")
	if err = s.completeKnowledgeLinks(*j, c, candidates, links); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale source published", err)
	}
	found, _ := s.Connections("", first, 0)
	if len(found) != 0 {
		t.Fatal(found)
	}
	// Early rule rejection results in zero metadata/link calls and no knowledge job.
	configureRules(t, s, .8, []FilterRule{{ID: "block", Reason: "fixture", Field: "url", Contains: "blocked", Probability: .9}})
	add(t, s, "https://example.com/blocked")
	calls := g.MetadataCalls + g.LinkCalls
	if worked, err := w.Step(context.Background()); err != nil || worked || g.MetadataCalls+g.LinkCalls != calls {
		t.Fatal(worked, err, g)
	}
}
func TestKnowledgeUIExportAndReadOnlyGuards(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	id := knowledgeArticle(t, s, "ui")
	g := &fixtureKnowledgeGenerator{}
	knowledgeStep(t, &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second})
	page := webRequest(w, "GET", "/items/1", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Теги моделі") || !strings.Contains(page.Body.String(), "Як відновити узгоджену копію?") {
		t.Fatal(page.Code, page.Body.String())
	}
	exported := webRequest(w, "GET", "/items/1/knowledge/markdown", nil, nil)
	if exported.Code != 200 || !strings.Contains(exported.Body.String(), "category:") {
		t.Fatal(exported.Code, exported.Body.String())
	}
	post := webRequest(w, "POST", "/items/1/knowledge-retry", url.Values{"csrf": {w.CSRF}}, map[string]string{"Authorization": "Bearer agent"})
	if post.Code != 403 {
		t.Fatal(post.Code)
	}
	w.ReadToken = strings.Repeat("r", 32)
	response := webRequest(w, "GET", "/api/read/items/1", nil, map[string]string{"Authorization": "Bearer " + w.ReadToken})
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Зберігання даних") || g.MetadataCalls != 1 {
		t.Fatal(response.Code, response.Body.String(), g)
	}
	response = webRequest(w, "GET", "/api/read/items/1/context?limit=128", nil, map[string]string{"Authorization": "Bearer " + w.ReadToken})
	var contextPayload map[string]any
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &contextPayload) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	metadata, ok := contextPayload["knowledge"].(map[string]any)
	if !ok || metadata["category"] != "Зберігання даних" || metadata["Markdown"] != nil || metadata["markdown"] != nil {
		t.Fatal(metadata)
	}
	var path string
	s.DB.QueryRow("SELECT markdown_path FROM article_knowledge WHERE item_id=?", id).Scan(&path)
	if err := os.Remove(filepath.Join(s.Files.Root, path)); err != nil {
		t.Fatal(err)
	}
	page = webRequest(w, "GET", "/items/1", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Markdown аналізу недоступний") || !strings.Contains(page.Body.String(), "Текст залишається доступним.") {
		t.Fatal(page.Code, page.Body.String())
	}
	item, _ := s.Item(id)
	if item.Read || item.Score.Valid {
		t.Fatal(item)
	}
}

func TestKnowledgeQueueRestartRecoveryAndBoundedTemporaryRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	id := knowledgeArticle(t, s, "restart")
	j, err := s.claimKnowledge(time.Second)
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	if _, err = s.DB.Exec("UPDATE knowledge_runs SET lease_until=0 WHERE id=?", j.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	next, err := s.claimKnowledge(time.Second)
	if err != nil || next == nil || next.Token == j.Token {
		t.Fatal(next, err)
	}
	digest, _ := s.Digest(next.DigestID, next.ItemID)
	metadata := ArticleKnowledge{Category: "Storage", Tags: []string{"sqlite"}, Questions: []string{"How to restore?"}}
	if err = s.completeKnowledgeMetadata(*j, metadata, digest); !errors.Is(err, ErrLeaseLost) {
		t.Fatal(err)
	}
	if err = s.failKnowledge(*next, &SourceError{Kind: "temporary", Retry: true, Message: "rate limit"}); err != nil {
		t.Fatal(err)
	}
	var state string
	s.DB.QueryRow("SELECT state FROM knowledge_runs WHERE id=?", next.ID).Scan(&state)
	if state != "retry_wait" {
		t.Fatal(state)
	}
	s.DB.Exec("UPDATE knowledge_runs SET next_attempt=0 WHERE id=?", next.ID)
	final, err := s.claimKnowledge(time.Second)
	if err != nil || final == nil || final.Attempts != 3 {
		t.Fatal(final, err)
	}
	s.DB.Exec("UPDATE knowledge_runs SET lease_until=0 WHERE id=?", final.ID)
	if claimed, err := s.claimKnowledge(time.Second); err != nil || claimed != nil {
		t.Fatal(claimed, err)
	}
	status, err := s.KnowledgeStatus(id, final.SnapshotID)
	if err != nil || status.State != "failed" || status.Kind != "unknown" {
		t.Fatal(status, err)
	}
	var events int
	s.DB.QueryRow("SELECT count(*) FROM knowledge_failures WHERE run_id=?", final.ID).Scan(&events)
	if events != 2 {
		t.Fatal(events)
	}
}

func TestKnowledgeGroupedThemeAndManualRevisionProvenance(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	first := knowledgeArticle(t, s, "first")
	second := knowledgeArticle(t, s, "second")
	third := knowledgeArticle(t, s, "third")
	var j *KnowledgeRun
	for range 3 {
		claimed, err := s.claimKnowledge(time.Second)
		if err != nil || claimed == nil {
			t.Fatal(claimed, err)
		}
		d, _ := s.Digest(claimed.DigestID, claimed.ItemID)
		k := ArticleKnowledge{Category: "Storage", Tags: []string{"sqlite"}, Questions: []string{"How to recover?"}}
		if err = s.completeKnowledgeMetadata(*claimed, k, d); err != nil {
			t.Fatal(err)
		}
		if claimed.ItemID == third {
			j = claimed
			break
		}
		c, _ := s.knowledgeContext(claimed.ItemID, claimed.SnapshotID, claimed.DigestID)
		if err = s.completeKnowledgeLinks(*claimed, c, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if j == nil {
		t.Fatal("third job missing")
	}
	c, _ := s.knowledgeContext(third, j.SnapshotID, j.DigestID)
	candidates, err := s.knowledgeCandidates(c)
	if err != nil || len(candidates) != 2 {
		t.Fatal(candidates, err)
	}
	g := &fixtureKnowledgeGenerator{}
	links := []KnowledgeLink{}
	for _, other := range candidates {
		l, _ := g.Links(context.Background(), j.Model, c, []KnowledgeContext{other})
		links = append(links, l[0])
	}
	if err = s.completeKnowledgeLinks(*j, c, candidates, links); err != nil {
		t.Fatal(err)
	}
	var note ConnectionNote
	for _, id := range []int64{first, second, third} {
		summaries, err := s.Connections("", id, 0)
		if err != nil || len(summaries) != 1 {
			t.Fatal(summaries, err)
		}
		note, err = s.Connection(summaries[0].ID, 0)
		if err != nil || len(note.Members) != 3 || note.Author != "model" {
			t.Fatal(note, err)
		}
	}
	original := note
	note.Body += "\nУточнення користувача."
	if _, err = s.SaveConnection(note, note.VersionID); err != nil {
		t.Fatal(err)
	}
	current, err := s.Connection(note.ID, 0)
	if err != nil || current.Author != "user" || current.Model != "" {
		t.Fatal(current, err)
	}
	historical, err := s.Connection(original.ID, original.VersionID)
	if err != nil || historical.Author != "model" || historical.Markdown != original.Markdown {
		t.Fatal(historical, err)
	}
}
func TestKnowledgeCloudJSONValidationAndNoLocalFallback(t *testing.T) {
	replies := []string{`{}`, `{"links":null}`, `{"links":[]}`, `{"links":[],"unexpected":true}`, "```json\n{\"links\":[]}\n```", "```\n{\"links\":[]}\n```", "before\n```json\n{\"links\":[]}\n```", "```json\n{\"links\":[],\"unexpected\":true}\n```", "```json\n{\"links\":[]} {}\n```", "```json\n{\"links\":[]"}
	for _, reply := range replies {
		t.Run(reply, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				rw.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					fmt.Fprint(rw, `{"models":[{"name":"fixture:cloud","remote_model":"fixture","remote_host":"https://ollama.com"}]}`)
				} else {
					json.NewEncoder(rw).Encode(map[string]any{"done": true, "message": map[string]string{"content": reply}})
				}
			}))
			defer server.Close()
			gateway, err := NewOllama(server.URL+"/api/chat", "", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			links, err := gateway.Links(context.Background(), "fixture:cloud", KnowledgeContext{}, nil)
			if unwrapKnowledgeJSON(reply) == `{"links":[]}` {
				if err != nil || len(links) != 0 {
					t.Fatal(links, err)
				}
			} else {
				var cause *SourceError
				if !errors.As(err, &cause) || cause.Kind != "format" || cause.Diagnostic != reply {
					t.Fatal(err)
				}
			}
			if _, err = gateway.Metadata(context.Background(), "local:latest", KnowledgeContext{}, nil); err == nil {
				t.Fatal("local generation permitted")
			}
		})
	}
}

func TestKnowledgeRetryUsesExactCheckpointAfterModelSwitch(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &fixtureKnowledgeGenerator{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	knowledgeArticle(t, s, "seed")
	knowledgeStep(t, w)
	id := knowledgeArticle(t, s, "switch")
	g.FailLinks = true
	knowledgeStep(t, w)
	item, _ := s.Item(id)
	first, err := s.Knowledge(id, item.Selected.Int64)
	if err != nil {
		t.Fatal(err)
	}
	s.DigestConfig.Model = "other:cloud"
	if err = s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	knowledgeStep(t, w)
	second, err := s.Knowledge(id, item.Selected.Int64)
	if err != nil || second.RunID == first.RunID || second.Model != "other:cloud" {
		t.Fatal(second, err)
	}
	s.DigestConfig.Model = "fixture:cloud"
	if err = s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	selected, err := s.Knowledge(id, item.Selected.Int64)
	if err != nil || selected.RunID != first.RunID {
		t.Fatal(selected, err)
	}
	g.FailLinks = false
	knowledgeStep(t, w)
	status, err := s.KnowledgeStatus(id, item.Selected.Int64)
	if err != nil || status.ID != first.RunID || status.State != "done" || g.MetadataCalls != 3 {
		t.Fatal(status, g, err)
	}
}

func TestKnowledgeMetadataFencedJSONStillValidatesSchema(t *testing.T) {
	valid := `{"category":"Storage","tags":["backup"],"questions":["How to restore?"]}`
	reply := ""
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(rw, `{"models":[{"name":"fixture:cloud","remote_model":"fixture","remote_host":"https://ollama.com"}]}`)
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"done": true, "message": map[string]string{"content": reply}})
	}))
	defer server.Close()
	gateway, err := NewOllama(server.URL+"/api/chat", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		raw      string
		accepted bool
	}{
		{valid, true}, {"```json\n" + valid + "\n```", true},
		{"```json\r\n" + valid + "\r\n```", true},
		{"```json\n" + valid + "\n```\nextra", false},
		{"```json\n" + strings.TrimSuffix(valid, "}") + `,"links":[]}` + "\n```", false},
		{"```json\n" + valid + "\n```\n```json\n" + valid + "\n```", false},
		{"```json\n{}\n```", false},
	} {
		reply = test.raw
		result, err := gateway.Metadata(context.Background(), "fixture:cloud", KnowledgeContext{}, nil)
		if test.accepted {
			if err != nil || result.Category != "Storage" {
				t.Fatal(test.raw, result, err)
			}
		} else {
			var cause *SourceError
			if !errors.As(err, &cause) || cause.Kind != "format" || cause.Diagnostic != reply {
				t.Fatal("schema/diagnostic changed", test.raw, err)
			}
		}
	}
}
