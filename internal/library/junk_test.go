package library

import (
	"database/sql"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestMarkJunkRatesZeroAndMovesToTrash(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/junk")
	manual(t, s, id, "Переклад чужого тексту без посилання на оригінал.")
	other := add(t, s, "https://example.com/keep")
	manual(t, s, other, "Корисний матеріал про переклад.")
	if err := s.MarkJunk(id, "неоформлений переклад"); err != nil {
		t.Fatal(err)
	}
	var score int
	var reason string
	var rated sql.NullString
	if err := s.DB.QueryRow("SELECT i.rating,i.rated_at,t.reason FROM items i JOIN trash_entries t ON t.item_id=i.id WHERE i.id=?", id).Scan(&score, &rated, &reason); err != nil {
		t.Fatal(err)
	}
	if score != 0 || !rated.Valid || !strings.Contains(reason, "неоформлений переклад") {
		t.Fatalf("rating %d %v %q", score, rated, reason)
	}
	if why, _ := s.TrashReason(id); !strings.Contains(why, "сміття") || !strings.Contains(why, "неоформлений переклад") {
		t.Fatalf("trash reason %q", why)
	}
	if _, err := s.Item(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a trashed item must not be reachable: %v", err)
	}
	res, err := s.Search(Filters{Query: "переклад"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range res {
		if item.ID == id {
			t.Fatal("junk must leave search")
		}
	}
	var texts int
	s.DB.QueryRow("SELECT count(*) FROM texts WHERE item_id=?", id).Scan(&texts)
	if texts != 1 {
		t.Fatal("source history must be kept")
	}
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatalf("repeat must be idempotent: %v", err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM items WHERE rating=0 AND id=?", id).Scan(&n)
	if n != 1 {
		t.Fatalf("repeat must not add rating events, got %d", n)
	}
}

func TestMarkJunkCancelsQueuedWork(t *testing.T) {
	s := testStore(t)
	enableDigestsMin(t, s, 0)
	id := add(t, s, "https://example.com/junk")
	manual(t, s, id, "Текст, що чекає на дайджест.")
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	var queued int
	s.DB.QueryRow("SELECT count(*) FROM jobs WHERE item_id=? AND state IN ('queued','retry_wait')", id).Scan(&queued)
	if queued != 0 {
		t.Fatalf("queued jobs must be cancelled, got %d", queued)
	}
}

func TestReaddingJunkURLIsRecognisedAsTrashed(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/junk")
	manual(t, s, id, "Сміття")
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	results, _ := s.AddURLs("https://example.com/junk")
	if len(results) != 1 || results[0].Status != "trashed" {
		t.Fatalf("%+v", results)
	}
}

func TestWebJunkRequiresConfirmation(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/junk")
	manual(t, s, id, "Неоформлений переклад.")
	path := "/items/" + strconv.FormatInt(id, 10) + "/junk"
	rw := webRequest(w, "POST", path, url.Values{"csrf": {w.CSRF}}, nil)
	if rw.Code != 400 {
		t.Fatalf("without confirmation: %d", rw.Code)
	}
	if why, _ := s.TrashReason(id); why != "" {
		t.Fatal("moved without confirmation")
	}
	post(t, w, path, url.Values{"confirm": {"1"}, "note": {"вода"}})
	if why, _ := s.TrashReason(id); !strings.Contains(why, "вода") {
		t.Fatalf("reason %q", why)
	}
	if rw := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil); rw.Code != 200 || !strings.Contains(rw.Body.String(), "/restore") {
		t.Fatalf("trashed card: %d", rw.Code)
	}
	if body := webRequest(w, "GET", "/trash", nil, nil).Body.String(); !strings.Contains(body, "example.com/junk") || !strings.Contains(body, "вода") {
		t.Fatalf("trash page:\n%s", body)
	}
}

func TestItemPageOffersJunkAction(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Звичайний матеріал.")
	if body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String(); !strings.Contains(body, "/junk") {
		t.Fatal("junk action missing")
	}
}
