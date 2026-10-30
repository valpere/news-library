package library

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func junkItem(t *testing.T, w *Web, id int64) string {
	t.Helper()
	page := "/items/" + strconv.FormatInt(id, 10)
	post(t, w, page+"/junk", url.Values{"confirm": {"1"}, "note": {"реклама"}})
	return page
}

func TestTrashedItemHasACardWithRestore(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/junk-card")
	manual(t, s, id, "Збережений текст матеріалу для картки помийки.")
	page := junkItem(t, w, id)
	rw := webRequest(w, "GET", page, nil, nil)
	body := rw.Body.String()
	if rw.Code != 200 {
		t.Fatalf("trashed card: %d", rw.Code)
	}
	for _, want := range []string{"https://example.com/junk-card", "реклама", "Збережений текст матеріалу", "Повернути", page + "/restore"} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	if strings.Contains(body, "Створити нову версію дайджесту") || strings.Contains(body, "/rate") {
		t.Error("a trashed card must not offer processing or rating")
	}
	if rw := webRequest(w, "GET", "/items/9999", nil, nil); rw.Code != 404 {
		t.Fatalf("unknown id: %d", rw.Code)
	}
}

func TestTrashListLinksToTheCard(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/junk-list")
	manual(t, s, id, "Текст для списку.")
	page := junkItem(t, w, id)
	if body := webRequest(w, "GET", "/trash", nil, nil).Body.String(); !strings.Contains(body, `href="`+page+`"`) {
		t.Fatalf("trash list does not link to the card:\n%s", body)
	}
}

func TestRestoreFromTrashReturnsTheItemAndKeepsHistory(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/junk-restore")
	manual(t, s, id, "Унікальнийсловоповернення у тексті.")
	page := junkItem(t, w, id)
	post(t, w, page+"/restore", url.Values{})
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM trash_entries WHERE item_id=?`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("still in trash: %d %v", n, err)
	}
	body := webRequest(w, "GET", page, nil, nil).Body.String()
	if strings.Contains(body, page+"/restore") || !strings.Contains(body, "Унікальнийсловоповернення") {
		t.Fatalf("restored item is not an ordinary card:\n%s", body)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM items WHERE id=? AND rating=0`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rating history must stay: %d %v", n, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM item_fts WHERE item_fts MATCH 'Унікальнийсловоповернення'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("search index not restored: %d %v", n, err)
	}
	if rw := webRequest(w, "POST", page+"/restore", url.Values{"csrf": {w.CSRF}}, nil); rw.Code != 404 {
		t.Fatalf("restoring a non-trashed item: %d", rw.Code)
	}
}
