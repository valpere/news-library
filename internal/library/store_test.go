package library

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "library.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func add(t *testing.T, s *Store, u string) int64 {
	t.Helper()
	id, e := s.AddURL(u)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func claim(t *testing.T, s *Store) Job {
	t.Helper()
	j, e := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if e != nil || j == nil {
		t.Fatalf("claim %v %v", j, e)
	}
	return *j
}
func manual(t *testing.T, s *Store, id int64, text string) {
	t.Helper()
	if e := s.Manual(id, text); e != nil {
		t.Fatal(e)
	}
}
func TestIngestConcurrentRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ids []int64
	for range 12 {
		wg.Go(func() {
			id, e := s.AddURL("https://example.com/article?id=4&utm_source=x#section")
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("duplicate items")
		}
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var n int
	if e = s.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&n); e != nil || n != 1 {
		t.Fatalf("jobs %d %v", n, e)
	}
	j := claim(t, s)
	if j.ItemID != ids[0] {
		t.Fatal("URL lost on restart")
	}
	if _, e = s.AddURL("https://example.com/article?id=4"); e != nil {
		t.Fatal(e)
	}
	next, e := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if e != nil || next != nil {
		t.Fatal("parallel duplicate job", e)
	}
	for _, u := range []string{"javascript:alert(1)", "http://user:pass@example.com/", "not-url"} {
		if _, e = s.AddURL(u); e == nil {
			t.Fatal("invalid URL accepted", u)
		}
	}
	other := add(t, s, "https://example.com/article?id=5")
	if other == j.ItemID {
		t.Fatal("meaningful query removed")
	}
}
func TestLeaseRestartRetryAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	id := add(t, s, "https://example.com/a")
	old := claim(t, s)
	if _, e = s.DB.Exec("UPDATE jobs SET lease_until=0 WHERE id=?", old.ID); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	j := claim(t, s)
	snap := Snapshot{Text: "Useful Ukrainian text пошук", Title: "Article", Origin: "fetch", Extractor: "fixture-v1", Completeness: "full"}
	if e = s.Complete(old, snap); e != ErrLeaseLost {
		t.Fatal("stale completion", e)
	}
	if e = s.Renew(old, time.Now(), time.Minute); e != ErrLeaseLost {
		t.Fatal("stale heartbeat", e)
	}
	if e = s.Complete(j, snap); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(j, snap); e != ErrLeaseLost {
		t.Fatal("replayed completion", e)
	}
	vs, e := s.Snapshots(id)
	if e != nil || len(vs) != 1 {
		t.Fatal("duplicate snapshot", e)
	}
	got, e := s.Search(Filters{Query: "пошук"})
	if e != nil || len(got) != 1 {
		t.Fatal("offline FTS", e)
	}
	if e = s.Retry(id); e != nil {
		t.Fatal(e)
	}
	j = claim(t, s)
	if e = s.Fail(j, "timeout", true, 3, time.Now(), time.Hour); e != nil {
		t.Fatal(e)
	}
	next, e := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if e != nil || next != nil {
		t.Fatal("backoff not honored")
	}
	if e = s.Retry(id); e == nil {
		t.Fatal("parallel retry accepted")
	}
	// Completing/failing a refresh never removes the available text.
	i, e := s.Item(id)
	if e != nil || i.State != "ready" || !i.Selected.Valid {
		t.Fatal("refresh lost text", e)
	}
}
func TestNeedsInputAndManualWinsRace(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	j := claim(t, s)
	if e := s.Fail(j, "no subtitles", false, 3, time.Now(), time.Second); e != nil {
		t.Fatal(e)
	}
	i, _ := s.Item(id)
	if i.State != "needs_input" || i.URL == "" {
		t.Fatal("missing source state")
	}
	if e := s.Retry(id); e != nil {
		t.Fatal(e)
	}
	j = claim(t, s)
	manual(t, s, id, "manual preferred")
	if e := s.Complete(j, Snapshot{Text: "older fetch", Origin: "fetch", Extractor: "fixture", Completeness: "full"}); e != nil {
		t.Fatal(e)
	}
	vs, _ := s.Snapshots(id)
	i, _ = s.Item(id)
	if len(vs) != 2 || i.Selected.Int64 != vs[1].ID {
		t.Fatal("older fetch overwrote manual")
	}
	matches, e := s.Search(Filters{Query: "preferred"})
	if e != nil || len(matches) != 1 {
		t.Fatal("manual missing from FTS", e)
	}
	if e = s.Retry(id); e != nil {
		t.Fatal(e)
	}
	j = claim(t, s)
	if e = s.Complete(j, Snapshot{Text: "explicit newer refresh", Origin: "fetch", Extractor: "fixture", Completeness: "full"}); e != nil {
		t.Fatal(e)
	}
	i, _ = s.Item(id)
	vs, _ = s.Snapshots(id)
	if i.Selected.Int64 != vs[0].ID {
		t.Fatal("explicit newer refresh not selected")
	}
}
func TestRatingHistoryAndIndependentActions(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	other := add(t, s, "https://example.com/b")
	manual(t, s, id, "first text")
	vs, _ := s.Snapshots(id)
	sid := vs[0].ID
	now := time.Now()
	if e := s.Rate(id, 3, &sid, &now, "useful"); e != nil {
		t.Fatal(e)
	}
	manual(t, s, id, "second version")
	if e := s.Rate(id, 0, nil, nil, "historical unknown date"); e != nil {
		t.Fatal(e)
	}
	for _, e := range []error{s.Rate(id, -1, nil, nil, ""), s.Rate(other, 2, &sid, nil, "")} {
		if e == nil {
			t.Fatal("invalid rating accepted")
		}
	}
	history, e := s.Ratings(id)
	if e != nil || len(history) != 2 || history[1].Score != 3 || history[1].SourceVersionID.Int64 != sid || history[0].AssessedAt.Valid {
		t.Fatal("history modified", e)
	}
	if _, e = s.DB.Exec("UPDATE rating_events SET score=8"); e == nil {
		t.Fatal("rating mutable")
	}
	if _, e = s.DB.Exec("DELETE FROM rating_events"); e == nil {
		t.Fatal("rating deletable")
	}
	if e = s.SetRead(id, true); e != nil {
		t.Fatal(e)
	}
	if e = s.SetTags(id, []string{"go", "news", "go"}); e != nil {
		t.Fatal(e)
	}
	i, e := s.Item(id)
	if e != nil || !i.Read || !i.Score.Valid || i.Score.Int64 != 0 {
		t.Fatal("independence", e)
	}
	unrated, e := s.Item(other)
	if e != nil || unrated.Score.Valid {
		t.Fatal("unrated became zero")
	}
	if e = s.SetRead(999, true); e != sql.ErrNoRows {
		t.Fatal("missing item", e)
	}
}
func TestFTSFiltersAndStableOrder(t *testing.T) {
	s := testStore(t)
	a := add(t, s, "https://example.com/a")
	b := add(t, s, "https://example.com/b")
	c := add(t, s, "https://example.com/c")
	for _, id := range []int64{a, b, c} {
		manual(t, s, id, "Ukraine knowledge store")
	}
	s.Rate(a, 0, nil, nil, "")
	s.Rate(b, 0, nil, nil, "")
	s.SetTags(a, []string{"one"})
	s.SetTags(b, []string{"two"})
	s.SetRead(b, true)
	for _, dir := range []string{"asc", "desc"} {
		got, e := s.Search(Filters{Sort: "rating", Direction: dir})
		if e != nil || len(got) != 3 || got[0].ID != a || got[1].ID != b || got[2].ID != c {
			t.Fatal("stable null order", e)
		}
	}
	got, e := s.Search(Filters{Query: "knowledge", Tags: []string{"one", "two"}, Read: "0", Rating: "0", State: "ready"})
	if e != nil || len(got) != 1 || got[0].ID != a {
		t.Fatal("AND/OR filters", e)
	}
	got, e = s.Search(Filters{Rating: "unrated"})
	if e != nil || len(got) != 1 || got[0].ID != c {
		t.Fatal("unrated filter", e)
	}
	got, e = s.Search(Filters{Query: "Ukraine", Sort: "relevance"})
	if e != nil || len(got) != 3 {
		t.Fatal("relevance", e)
	}
	if _, e = s.Search(Filters{Query: `" OR * :`}); e != nil {
		t.Fatal("literal user query failed", e)
	}
}
func TestCrashOnLastAttemptTerminates(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	j := claim(t, s)
	s.DB.Exec("UPDATE jobs SET attempts=3,lease_until=0 WHERE id=?", j.ID)
	next, e := s.Claim(context.Background(), time.Now(), time.Minute, 3)
	if e != nil || next != nil {
		t.Fatal("infinite final retry", e)
	}
	i, _ := s.Item(id)
	if i.State != "needs_input" {
		t.Fatal("crash not diagnosed")
	}
	// A poisoned item does not prevent the next one.
	b := add(t, s, "https://example.com/b")
	if claim(t, s).ItemID != b {
		t.Fatal("queue stalled")
	}
}
