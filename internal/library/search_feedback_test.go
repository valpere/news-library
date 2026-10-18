package library

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestSearchEventRecordingAndJudgments(t *testing.T) {
	s := testStore(t)
	a, b := add(t, s, "https://example.com/a"), add(t, s, "https://example.com/b")
	ev, err := s.RecordSearch("bloom filter", "fts", "fts", "usefulness=2", []int64{a, b})
	if err != nil || ev == 0 {
		t.Fatalf("%d %v", ev, err)
	}
	again, err := s.RecordSearch("bloom filter", "fts", "fts", "usefulness=2", []int64{a, b})
	if err != nil || again != ev {
		t.Fatalf("identical search within the window must reuse the event: %d %d %v", again, ev, err)
	}
	other, _ := s.RecordSearch("bloom filter", "fts", "fts", "usefulness=2", []int64{b, a})
	if other == ev {
		t.Fatal("different result order is a different event")
	}
	if err = s.JudgeSearchResult(ev, a, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.JudgeSearchResult(ev, a, 2); err != nil { // later judgment wins, history stays
		t.Fatal(err)
	}
	e, judgments, err := s.SearchEvent(ev)
	if err != nil || judgments[a] != 2 || len(judgments) != 1 || e.Query != "bloom filter" || len(e.ResultIDs) != 2 {
		t.Fatalf("%+v %v %v", e, judgments, err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM search_judgments WHERE event_id=?", ev).Scan(&n)
	if n != 2 {
		t.Fatalf("history must be append-only, got %d rows", n)
	}
	c := add(t, s, "https://example.com/c")
	for _, bad := range []struct {
		item  int64
		score int
	}{{c, 1}, {a, 3}, {a, -1}} {
		if s.JudgeSearchResult(ev, bad.item, bad.score) == nil {
			t.Fatalf("must reject %+v", bad)
		}
	}
	if err = s.MarkSearchNotFound(ev); err != nil {
		t.Fatal(err)
	}
	if e, _, _ = s.SearchEvent(ev); e.NotFoundAt == "" {
		t.Fatal("not found must be dated")
	}
}

func TestSearchJudgmentsDoNotChangeUsefulnessRatings(t *testing.T) {
	s := testStore(t)
	a := add(t, s, "https://example.com/a")
	ev, _ := s.RecordSearch("q", "fts", "fts", "", []int64{a})
	if err := s.JudgeSearchResult(ev, a, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM rating_events").Scan(&n)
	if n != 0 {
		t.Fatalf("search judgment created %d rating events", n)
	}
}

func TestWebSearchRecordsAndJudges(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/bloom")
	if err := s.Manual(id, "Bloom filter is a probabilistic data structure for set membership."); err != nil {
		t.Fatal(err)
	}
	rw := webRequest(w, "GET", "/browse?q=bloom", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "/search-events/") {
		t.Fatalf("%d %s", rw.Code, rw.Body.String())
	}
	var ev int64
	s.DB.QueryRow("SELECT id FROM search_events WHERE query='bloom'").Scan(&ev)
	if ev == 0 {
		t.Fatal("search was not recorded")
	}
	if rw := webRequest(w, "GET", "/browse", nil, nil); rw.Code != 200 {
		t.Fatal(rw.Code)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM search_events").Scan(&count)
	if count != 1 {
		t.Fatalf("browse without a query must not record, got %d events", count)
	}
	path := "/search-events/" + strconv.FormatInt(ev, 10)
	post(t, w, path+"/judgments", url.Values{"item": {strconv.FormatInt(id, 10)}, "score": {"2"}, "back": {"/browse?q=bloom"}})
	post(t, w, path+"/not-found", url.Values{"back": {"/browse?q=bloom"}})
	_, j, _ := s.SearchEvent(ev)
	if j[id] != 2 {
		t.Fatalf("%v", j)
	}
	v := url.Values{"item": {"1"}, "score": {"2"}, "back": {"https://evil.example/"}, "csrf": {w.CSRF}}
	rw = webRequest(w, "POST", path+"/judgments", v, nil)
	if rw.Code != http.StatusSeeOther || !strings.HasPrefix(rw.Header().Get("Location"), "/browse") {
		t.Fatalf("open redirect: %d %q", rw.Code, rw.Header().Get("Location"))
	}
}
