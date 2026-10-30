package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestChallengeNeverGeneratesAndArticleDiscussionIsAllowed(t *testing.T) {
	blocked := SourceText{Title: "Client Challenge", Text: "A required part of this site couldn’t load. Please check your connection."}
	o, _ := NewOllama("http://127.0.0.1:1/api/chat", "", time.Second)
	calls := 0
	o.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("generation must not be called")
	})
	if _, err := o.Generate(context.Background(), Job{}, blocked); err == nil {
		t.Fatal("challenge reached generation")
	} else {
		var se *SourceError
		if !errors.As(err, &se) || !se.Challenge || se.Kind != "source" || calls != 0 {
			t.Fatal("source guard did not prevent model call", err, calls)
		}
	}
	g := &ChunkGenerator{}
	if _, err := g.Generate(context.Background(), Job{}, blocked); err == nil {
		t.Fatal("challenge reached chunking")
	}
	if err := ValidateSourceContent(SourceText{Title: "Understanding Client Challenge", Text: blocked.Text + " This article describes the challenge protocol."}); err != nil {
		t.Fatal("article rejected", err)
	}
}
func TestChallengeBrowserFallbackIsBoundedAndUsesRealArticle(t *testing.T) {
	fallback := 0
	f := &Fetcher{MaxBytes: 4096, UserAgent: "fixture-agent", client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") != "fixture-agent" {
			t.Fatal("missing agent")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: ioBody("<html><title>Client Challenge</title><article><p>A required part of this site couldn’t load. Please check your connection.</p></article></html>"), Request: r}, nil
	})}, browserResolver: func(ctx context.Context, raw string) ([]byte, *url.URL, string, bool, error) {
		fallback++
		u, _ := url.Parse(raw)
		return []byte("full real article body"), u, "text/plain", false, nil
	}}
	s, err := f.Fetch(context.Background(), "https://example.com/article")
	if err != nil || s.Text != "full real article body" || fallback != 1 || !strings.Contains(s.Extractor, "curl") {
		t.Fatal(s, err, fallback)
	}
	f.browserResolver = func(ctx context.Context, raw string) ([]byte, *url.URL, string, bool, error) {
		fallback++
		u, _ := url.Parse(raw)
		return []byte("<html><title>Client Challenge</title><article><p>A required part of this site couldn’t load. Please check your connection.</p></article></html>"), u, "text/html", false, nil
	}
	_, err = f.Fetch(context.Background(), "https://example.com/article")
	var se *SourceError
	if !errors.As(err, &se) || !se.Challenge || se.Retry || fallback != 2 {
		t.Fatal("interstitial accepted or retried", err, fallback)
	}
}

func TestBrowserFallbackCannotBypassPrivateAddressGuard(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl unavailable")
	}
	hits := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write([]byte("private secret")) }))
	defer fixture.Close()
	t.Setenv("NO_PROXY", "*")
	f := NewFetcher(time.Second, 4096)
	if _, _, _, _, err := f.browserGet(context.Background(), fixture.URL); err == nil {
		t.Fatal("private target accepted")
	}
	if hits != 0 {
		t.Fatal("private target reached", hits)
	}
	if _, _, _, _, err := f.browserGet(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("file URL accepted")
	}
}

func TestScopedKnowledgeDoesNotClaimOrCancelOtherItems(t *testing.T) {
	s, w := batchFixture(t)
	if _, err := s.QueueKnowledgeBatch(strings.Repeat("z", 26), 5, true); err != nil {
		t.Fatal(err)
	}
	w.ItemID = 5
	knowledgeStep(t, w)
	var state string
	if err := s.DB.QueryRow("SELECT state FROM knowledge_runs WHERE item_id=5").Scan(&state); err != nil || state != "done" {
		t.Fatal(state, err)
	}
	// A stale job elsewhere must also remain untouched by scoped cleanup.
	if _, err := s.DB.Exec("UPDATE items SET filter_state='rejected' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.Step(context.Background()); err != nil || worked {
		t.Fatal(worked, err)
	}
	if err := s.DB.QueryRow("SELECT state FROM knowledge_runs WHERE item_id=1").Scan(&state); err != nil || state != "queued" {
		t.Fatal("unrelated work changed", state, err)
	}
}

func TestSavedChallengeMakesZeroEmbeddingCalls(t *testing.T) {
	s, sem, f := semanticFixture(t)
	id := add(t, s, "https://example.com/challenge")
	if _, err := s.DB.Exec("UPDATE items SET title='Client Challenge' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	manual(t, s, id, "A required part of this site couldn’t load. Please check your connection.")
	if worked, err := sem.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if f.calls != 0 {
		t.Fatal("challenge embedded", f.calls)
	}
	var state string
	if err := s.DB.QueryRow("SELECT state FROM embedding_runs").Scan(&state); err != nil || state != "failed" {
		t.Fatal(state, err)
	}
}

func TestCopyrightOnlyPreservedBeforeAnyAI(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/copyright-fixture")
	footer := "Copyright © 2014-2026 Example Open Source Project"
	u, _ := url.Parse("https://example.com/article")
	snap, err := decodeArticle([]byte("<html><title>Docs</title><article><p>"+footer+"</p></article></html>"), u, "text/html", false, &NetworkMeta{FetchedAt: nowUTC()})
	if err != nil || snap.Text != footer || !strings.Contains(snap.Warning, "copyright") {
		t.Fatal(snap, err)
	}
	calls := 0
	worker := Worker{Store: s, Source: sourceFunc(func(context.Context, string) (SourceText, error) { return snap, nil }), Generator: generatorFunc(func(context.Context, Job, SourceText) (Digest, error) { calls++; return Digest{}, nil }), Timeout: time.Second, Lease: time.Minute, MaxAttempts: 3}
	if worked, err := worker.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	item, _ := s.Item(id)
	if item.State != "needs_input" || !item.HasText || !strings.Contains(item.Error, "copyright") {
		t.Fatal(item)
	}
	saved, _ := s.SourceText(item.ID)
	if saved.Text != footer {
		t.Fatal("extracted notice lost", saved)
	}
	if worked, err := worker.Step(context.Background()); err != nil || worked || calls != 0 {
		t.Fatal("queued AI or automatic retry", worked, err, calls)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE stage='digest'").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var jobID int64
	s.DB.QueryRow("SELECT id FROM jobs WHERE item_id=?", id).Scan(&jobID)
	failures, err := s.JobFailures(jobID)
	if err != nil || len(failures) != 1 || failures[0].Kind != "source" || failures[0].Retryable {
		t.Fatal(failures, err)
	}
	// Replacing an insufficient source with real manual text erases the rating of the old one.
	if err := s.Rate(id, 3, nil); err != nil {
		t.Fatal(err)
	}
	manual(t, s, id, "A meaningful short announcement describes AI agents for defenders.")
	item, _ = s.Item(id)
	if item.State != "enriching" {
		t.Fatal(item)
	}
	snapshots, _ := s.Texts(id)
	if len(snapshots) != 1 || snapshots[0].Text == footer {
		t.Fatal("replaced text kept", snapshots)
	}
	if ratings, _ := s.Ratings(id); len(ratings) != 0 {
		t.Fatal("rating survived a text change", ratings)
	}
}

func TestCopyrightGuardDoesNotRejectShortContent(t *testing.T) {
	for _, text := range []string{"Copyright © 2026 Example Project", "© 2026 Example Project\nAll rights reserved.", "Copyright (c) 2026 Example Project. All rights reserved."} {
		if err := ValidateSourceContent(SourceText{Text: text}); err == nil {
			t.Fatal("notice accepted", text)
		}
	}
	for _, text := range []string{"AI agents help defenders detect threats.", "An abstract describes a new embedding method.", "Copyright © 2026 Example Project\nThis article explains copyright law.", "Copyright law changed: developers should check licenses.", "The footer says Copyright © 2026 Example Project."} {
		if err := ValidateSourceContent(SourceText{Text: text}); err != nil {
			t.Fatal("meaningful short source rejected", text, err)
		}
	}
}

func TestSavedCopyrightMakesZeroGenerationAndEmbeddingCalls(t *testing.T) {
	footer := SourceText{Text: "Copyright © 2026 Example Project"}
	o, _ := NewOllama("http://127.0.0.1:1/api/chat", "", time.Second)
	calls := 0
	o.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("no model call expected") })
	if _, err := o.Generate(context.Background(), Job{}, footer); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := (&ChunkGenerator{}).Generate(context.Background(), Job{}, footer); err == nil {
		t.Fatal("footer reached chunking")
	}
	s, sem, f := semanticFixture(t)
	id := add(t, s, "https://example.com/saved-footer")
	manual(t, s, id, footer.Text)
	if worked, err := sem.Step(context.Background()); err != nil || !worked || f.calls != 0 {
		t.Fatal(worked, err, f.calls)
	}
	if _, err := s.knowledgeContext(id); err == nil {
		t.Fatal("footer reached knowledge input")
	}
}

func TestFreshFooterKeepsExistingSelectedSource(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/fresh-footer")
	first, _ := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	original := SourceText{Title: "Real article", Text: "AI agents help defenders detect threats.", Origin: "fetch", Extractor: "fixture", Completeness: "full"}
	if err := s.Complete(*first, original); err != nil {
		t.Fatal(err)
	}
	completeFixtureDigest(t, s)
	before, _ := s.Item(id)
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	fresh, _ := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if err := s.Complete(*fresh, SourceText{Title: "Footer", Text: "Copyright © 2026 Example Project", Origin: "fetch", Extractor: "fixture", Completeness: "unknown"}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Item(id)
	if after.HasText != before.HasText || after.State != before.State || after.Title != before.Title {
		t.Fatal("good selected source replaced", before, after)
	}
	snapshots, _ := s.Texts(id)
	if len(snapshots) != 1 || strings.Contains(snapshots[0].Text, "Copyright") {
		t.Fatal("a notice replaced or joined the good source", snapshots)
	}
	failures, _ := s.JobFailures(fresh.ID)
	if len(failures) != 1 || failures[0].Kind != "source" {
		t.Fatal(failures)
	}
}
