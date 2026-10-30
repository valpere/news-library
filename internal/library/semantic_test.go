package library

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fixtureEmbedder struct {
	revision string
	calls    int
	fail     bool
}

func (f *fixtureEmbedder) Revision(context.Context) (string, error) { return f.revision, nil }
func (f *fixtureEmbedder) Embed(_ context.Context, text, revision string) ([]float64, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("fixture unavailable")
	}
	if revision != f.revision {
		return nil, errors.New("changed revision")
	}
	if strings.Contains(text, "database") || strings.Contains(text, "storage") || strings.Contains(text, "persist") {
		return []float64{1, 0}, nil
	}
	return []float64{0, 1}, nil
}
func semanticFixture(t *testing.T) (*Store, *Semantic, *fixtureEmbedder) {
	t.Helper()
	s := testStore(t)
	f := &fixtureEmbedder{revision: "fixture-v1"}
	return s, &Semantic{Store: s, Embedder: f, ChunkBytes: 1024, Dimensions: 2, Timeout: time.Second}, f
}
func drainIndex(t *testing.T, semantic *Semantic) {
	t.Helper()
	for n := 0; n < 30; n++ {
		worked, err := semantic.Step(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("index did not finish")
}
func TestSemanticSynonymFiltersHybridAndOfflineFTS(t *testing.T) {
	s, semantic, f := semanticFixture(t)
	id := add(t, s, "https://example.com/database")
	if err := s.Manual(id, "database survives restart without losing data"); err != nil {
		t.Fatal(err)
	}
	s.SetTags(id, []string{"systems"})
	other := add(t, s, "https://example.com/weather")
	s.Manual(other, "weather forecast")
	s.Rate(other, 0, nil)
	drainIndex(t, semantic)
	zero, e := semantic.Search(context.Background(), Filters{Query: "weather", Usefulness: "0"}, "vector")
	if e != nil || len(zero.Items) != 1 || zero.Items[0].ID != other || !zero.Items[0].Score.Valid || zero.Items[0].Score.Int64 != 0 {
		t.Fatal("zero rating filter", zero, e)
	}
	before := f.calls
	got, err := semantic.Search(context.Background(), Filters{Query: "persistent storage", Tags: []string{"systems"}, Usefulness: "unrated"}, "vector")
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != id || got.Items[0].MatchSnapshot == 0 || !strings.Contains(got.Items[0].MatchSnippet, "database") {
		t.Fatal(got, err)
	}
	if f.calls != before+1 {
		t.Fatal("unexpected query inference")
	}
	got, err = semantic.Search(context.Background(), Filters{Query: "database"}, "hybrid")
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != id {
		t.Fatal(got, err)
	}
	f.fail = true
	got, err = semantic.Search(context.Background(), Filters{Query: "database"}, "hybrid")
	if err != nil || got.Mode != "fts" || got.Warning == "" || len(got.Items) != 1 {
		t.Fatal(got, err)
	}
	before = f.calls
	got, err = semantic.Search(context.Background(), Filters{Query: "database"}, "fts")
	if err != nil || len(got.Items) != 1 || f.calls != before {
		t.Fatal("FTS calls embeddings", got, err)
	}
	item, _ := s.Item(id)
	if item.Read || item.Score.Valid {
		t.Fatal("search mutated human state")
	}
	f.fail = false
	f.revision = "fixture-v2"
	got, err = semantic.Search(context.Background(), Filters{Query: "database"}, "vector")
	if err != nil || got.Mode != "fts" {
		t.Fatal("mixed model revisions", got, err)
	}
	drainIndex(t, semantic)
	got, err = semantic.Search(context.Background(), Filters{Query: "persistent storage"}, "vector")
	if err != nil || len(got.Items) != 1 {
		t.Fatal(got, err)
	}
	// A new primary snapshot must never match an older semantic index.
	s.Manual(id, "weather changed source")
	got, err = semantic.Search(context.Background(), Filters{Query: "persistent storage"}, "vector")
	if err != nil || len(got.Items) != 0 {
		t.Fatal("stale snapshot index used", got, err)
	}
}
func TestEmbeddingCheckpointRestartRejectAndLeaseFencing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixtureEmbedder{revision: "v1"}
	semantic := &Semantic{Store: s, Embedder: f, ChunkBytes: 1024, Dimensions: 2, Timeout: time.Second}
	id := add(t, s, "https://example.com/long-index")
	s.Manual(id, strings.Repeat("database ", 200))
	if worked, e := semantic.Step(context.Background()); e != nil || !worked {
		t.Fatal(worked, e)
	}
	f.fail = true
	semantic.Step(context.Background())
	calls := f.calls
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	semantic.Store = s
	s.DB.Exec("UPDATE embedding_runs SET next_attempt=0")
	f.fail = false
	drainIndex(t, semantic)
	if f.calls != calls+1 {
		t.Fatal("completed chunk repeated", calls, f.calls)
	}
	rules, _ := NewRuleSet(.8, `[{"id":"ad","reason":"ad","field":"text","contains":"advertisement","probability":1}]`)
	s.ConfigureRules(rules)
	rejected := add(t, s, "https://example.com/ad")
	s.Manual(rejected, "advertisement database")
	calls = f.calls
	drainIndex(t, semantic)
	if f.calls != calls {
		t.Fatal("rejected source embedded")
	}
	s.OverrideFilter(rejected)
	drainIndex(t, semantic)
	if f.calls != calls+1 {
		t.Fatal("explicit override not indexed")
	}
	s.Manual(id, "database new source")
	semantic.schedule("v1")
	job, err := semantic.claim("v1")
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	s.DB.Exec("UPDATE embedding_runs SET lease_until=0 WHERE item_id=? AND version=?", job.SnapshotID, job.Version)
	if err = semantic.finish(*job, []string{"database new source"}, []float64{1, 0}); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale worker published", err)
	}
	drainIndex(t, semantic)
}
func TestReadAPIAuthenticationContextAndNoMutations(t *testing.T) {
	s, w := testWeb(t)
	token := strings.Repeat("r", 32)
	w.ReadToken = token
	id := add(t, s, "https://example.com/read")
	s.Manual(id, strings.Repeat("Український текст. ", 400))
	s.Rate(id, 3, nil)
	auth := map[string]string{"Authorization": "Bearer " + token}
	for _, headers := range []map[string]string{nil, {"Authorization": "Bearer wrong"}} {
		rw := webRequest(w, "GET", "/api/read/items/1/context", nil, headers)
		if rw.Code != 401 {
			t.Fatal(rw.Code)
		}
	}
	rw := webRequest(w, "GET", "/api/read/items/1/context", nil, auth)
	if rw.Code != 200 {
		t.Fatal(rw.Code, rw.Body.String())
	}
	var got map[string]any
	json.Unmarshal(rw.Body.Bytes(), &got)
	if got["truncated"] != true || got["context_kind"] != "source_excerpt" || got["rating"] != float64(3) {
		t.Fatal(got)
	}
	if got["rated_at"] != nil {
		t.Fatal("invented historical date")
	}
	rw = webRequest(w, "GET", "/api/read/items/1/fragment?start=1&end=7", nil, auth)
	if rw.Code != 400 {
		t.Fatal("split UTF8 accepted", rw.Code)
	}
	rw = webRequest(w, "GET", "/api/read/items/1/fragment?start=1&end=1", nil, auth)
	if rw.Code != 400 {
		t.Fatal("empty fragment inside UTF8 accepted", rw.Code)
	}
	rw = webRequest(w, "GET", "/api/read/items/1/fragment?start=0&end=20", nil, auth)
	if rw.Code != 200 {
		t.Fatal(rw.Code)
	}
	for _, path := range []string{"/api/read/items/1", "/items/1/read", "/reindex"} {
		rw = webRequest(w, "POST", path, url.Values{"csrf": {w.CSRF}, "read": {"1"}}, auth)
		if rw.Code < 400 {
			t.Fatal("agent mutation accepted", path, rw.Code)
		}
	}
	rw = webRequest(w, "GET", "/api/read/search?q=Український", nil, auth)
	if rw.Code != 200 {
		t.Fatal(rw.Code)
	}
	item, _ := s.Item(id)
	ratings, _ := s.Ratings(id)
	if item.Read || item.Score.Int64 != 3 || len(ratings) != 1 {
		t.Fatal("read mutated history")
	}
}
func fmtID(id int64) string { return strconv.FormatInt(id, 10) }
func TestBGEAdapterOnlyLocalEmbeddingAndValidatesVectors(t *testing.T) {
	var calls int
	invalid := false
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(rw).Encode(map[string]any{"models": []any{map[string]any{"name": "bge-m3:latest", "digest": "fixture-revision"}}})
			return
		}
		if r.URL.Path != "/api/embed" {
			t.Error("generation endpoint called", r.URL.Path)
		}
		calls++
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "bge-m3:latest" || payload["truncate"] != false {
			t.Error(payload)
		}
		vector := make([]float64, 1024)
		vector[0] = 1
		if invalid {
			vector = vector[:2]
		}
		json.NewEncoder(rw).Encode(map[string]any{"model": "bge-m3:latest", "embeddings": [][]float64{vector}})
	}))
	defer server.Close()
	bge, err := NewBGE(server.URL+"/api/embed", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := bge.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	vector, err := bge.Embed(context.Background(), "fixture", revision)
	if err != nil || len(vector) != 1024 || vector[0] != 1 || calls != 1 {
		t.Fatal(vector, err, calls)
	}
	invalid = true
	if _, err = bge.Embed(context.Background(), "fixture", revision); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
	for _, endpoint := range []string{"https://external.example/api/embed", server.URL + "/api/chat", server.URL + "/api/embed?secret=x"} {
		if _, err = NewBGE(endpoint, time.Second); err == nil {
			t.Fatal("unsafe endpoint", endpoint)
		}
	}
	for _, vector := range [][]float64{{0, 0}, {math.NaN(), 1}, {math.Inf(1), 1}} {
		if _, err = normalized(vector, 2); err == nil {
			t.Fatal("invalid vector accepted")
		}
	}
}

func TestExplicitRebuildPreservesHumanHistoryAndMigratedFilterGuard(t *testing.T) {
	s, semantic, _ := semanticFixture(t)
	id := add(t, s, "https://example.com/rebuild")
	manual(t, s, id, "database source")
	item, _ := s.Item(id)
	sid := item.Selected.Int64
	if err := s.Rate(id, 4, nil); err != nil {
		t.Fatal(err)
	}
	drainIndex(t, semantic)
	if err := s.RebuildEmbeddings(); err != nil {
		t.Fatal(err)
	}
	drainIndex(t, semantic)
	ratings, _ := s.Ratings(id)
	after, _ := s.Item(id)
	if len(ratings) != 1 || ratings[0].AssessedAt.Valid || after.Selected.Int64 != sid || after.Score.Int64 != 4 {
		t.Fatal("rebuild changed primary context")
	}
}

func TestReadContextUsesSavedDigestAndCorrectBounds(t *testing.T) {
	s, w := testWeb(t)
	w.ReadToken = strings.Repeat("r", 32)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/digest-context")
	manual(t, s, id, strings.Repeat("Original source. ", 500))
	completeFixtureDigest(t, s)
	rw := webRequest(w, "GET", "/api/read/items/1/context", nil, map[string]string{"Authorization": "Bearer " + w.ReadToken})
	if rw.Code != 200 {
		t.Fatal(rw.Code)
	}
	var got map[string]any
	json.Unmarshal(rw.Body.Bytes(), &got)
	if got["text"] != fixtureDigest || got["truncated"] != false || got["text_kind"] != "digest" || got["digest"] == nil || got["source"] == nil {
		t.Fatal(got)
	}
	jobs, _ := s.Jobs(id)
	if len(jobs) != 2 {
		t.Fatal("read queued generation", jobs)
	}
}
