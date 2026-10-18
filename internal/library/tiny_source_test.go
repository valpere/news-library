package library

import (
	"strconv"
	"strings"
	"testing"
)

func enableDigestsMin(t *testing.T, s *Store, min int) {
	t.Helper()
	if e := s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 4096, MinSourceWords: min}); e != nil {
		t.Fatal(e)
	}
}

func digestJobs(t *testing.T, s *Store, id int64) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND stage='digest'", id).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// Reading a handful of words takes seconds, so a digest cannot save time:
// below the threshold the source itself is the content, with zero model calls.
func TestTinySourceGetsNoDigestJob(t *testing.T) {
	s := testStore(t)
	enableDigestsMin(t, s, 40)
	tiny := add(t, s, "https://example.com/tiny")
	manual(t, s, tiny, strings.Repeat("слово ", 39))
	edge := add(t, s, "https://example.com/edge")
	manual(t, s, edge, strings.Repeat("слово ", 40))
	if n := digestJobs(t, s, tiny); n != 0 {
		t.Fatalf("tiny source must not queue a digest, got %d jobs", n)
	}
	if i, _ := s.Item(tiny); i.State == "enriching" || i.State == "digest_failed" {
		t.Fatalf("tiny source state %q", i.State)
	}
	if n := digestJobs(t, s, edge); n != 1 {
		t.Fatalf("source at the threshold keeps its digest, got %d jobs", n)
	}
}

func TestMinSourceWordsZeroKeepsEveryDigest(t *testing.T) {
	s := testStore(t)
	enableDigestsMin(t, s, 0)
	id := add(t, s, "https://example.com/short")
	manual(t, s, id, "Три слова тут")
	if n := digestJobs(t, s, id); n != 1 {
		t.Fatalf("threshold 0 disables the cutoff, got %d jobs", n)
	}
}

func TestTinySourcePageExplainsAndAllowsRating(t *testing.T) {
	s, w := testWeb(t)
	enableDigestsMin(t, s, 40)
	id := add(t, s, "https://example.com/tiny")
	manual(t, s, id, "Дуже короткий текст без потреби в дайджесті.")
	body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	if !strings.Contains(body, "Дайджест не потрібен") {
		t.Fatalf("missing explanation:\n%s", body)
	}
	if strings.Contains(body, "Генерацію не завершено") {
		t.Fatal("misleading unfinished-generation message")
	}
	if !strings.Contains(body, "/rating") {
		t.Fatal("a tiny source must stay ratable")
	}
}
