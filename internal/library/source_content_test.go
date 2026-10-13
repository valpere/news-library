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
	blocked := Snapshot{Title: "Client Challenge", Text: "A required part of this site couldn’t load. Please check your connection."}
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
	if err := ValidateSourceContent(Snapshot{Title: "Understanding Client Challenge", Text: blocked.Text + " This article describes the challenge protocol."}); err != nil {
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
	if _, err := s.DB.Exec("UPDATE items SET selected_snapshot=NULL WHERE id=1"); err != nil {
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
