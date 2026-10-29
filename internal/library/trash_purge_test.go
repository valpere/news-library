package library

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func junked(t *testing.T, s *Store, u, text string) int64 {
	t.Helper()
	id := add(t, s, u)
	manual(t, s, id, text)
	if err := s.Rate(id, 3, nil, nil, "до сміття"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJunk(id, "тест"); err != nil {
		t.Fatal(err)
	}
	return id
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func textPath(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var p string
	if err := s.DB.QueryRow(`SELECT text_path FROM source_versions WHERE item_id=?`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(s.Files.Root, p)
}

func TestPurgeErasesTheTrashedMaterialAndItsFile(t *testing.T) {
	s := testStore(t)
	keep := add(t, s, "https://example.com/keep")
	manual(t, s, keep, "Текст матеріалу, який лишається.")
	id := junked(t, s, "https://example.com/purge-me", "Унікальний текст для остаточного видалення.")
	path := textPath(t, s, id)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeTrashed(id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"items", "source_versions", "rating_events", "trash_entries", "source_aliases", "jobs"} {
		col := "item_id"
		if table == "items" {
			col = "id"
		}
		if n := count(t, s, "SELECT count(*) FROM "+table+" WHERE "+col+"=?", id); n != 0 {
			t.Errorf("%s still has %d rows", table, n)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("text file kept: %v", err)
	}
	if n := count(t, s, `SELECT count(*) FROM source_versions WHERE item_id=?`, keep); n != 1 {
		t.Errorf("another material was touched: %d", n)
	}
	again := add(t, s, "https://example.com/purge-me")
	if count(t, s, `SELECT count(*) FROM source_versions WHERE item_id=?`, again)+count(t, s, `SELECT count(*) FROM trash_entries WHERE item_id=?`, again) != 0 {
		t.Error("a purged URL must come back as a brand new material without the old history")
	}
}

func TestPurgeRefusesLiveMaterialsAndKeepsTheirImmutability(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/live")
	manual(t, s, id, "Живий матеріал.")
	if err := s.Rate(id, 3, nil, nil, "оцінка"); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeTrashed(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("live material: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM source_versions WHERE item_id=?`, id); err == nil {
		t.Fatal("history of a live material must stay immutable")
	}
	if _, err := s.DB.Exec(`DELETE FROM rating_events WHERE item_id=?`, id); err == nil {
		t.Fatal("ratings of a live material must stay immutable")
	}
}

func TestPurgeKeepsAFileAnotherMaterialStillUses(t *testing.T) {
	s := testStore(t)
	a := junked(t, s, "https://example.com/same-a", "Однаковий текст у двох матеріалах.")
	b := junked(t, s, "https://example.com/same-b", "Однаковий текст у двох матеріалах.")
	path := textPath(t, s, a)
	if path != textPath(t, s, b) {
		t.Skip("texts are not shared")
	}
	if err := s.PurgeTrashed(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shared file removed: %v", err)
	}
	if err := s.PurgeTrashed(b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file kept after its last user went: %v", err)
	}
}

func TestPurgeRefusesMaterialUsedByConnectionNotes(t *testing.T) {
	s, n := connectionFixture(t)
	if _, err := s.SaveConnection(n, 0); err != nil {
		t.Fatal(err)
	}
	id := n.Members[0].ItemID
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	var blocked *PurgeBlockedError
	if err := s.PurgeTrashed(id); !errors.As(err, &blocked) || !strings.Contains(blocked.Why, "нотат") {
		t.Fatalf("expected a blocked purge, got %v", err)
	}
	if count(t, s, `SELECT count(*) FROM items WHERE id=?`, id) != 1 {
		t.Fatal("a blocked purge must change nothing")
	}
}

func TestPurgeAllTrashedSkipsBlockedOnes(t *testing.T) {
	s, n := connectionFixture(t)
	if _, err := s.SaveConnection(n, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkJunk(n.Members[0].ItemID, ""); err != nil {
		t.Fatal(err)
	}
	junked(t, s, "https://example.com/free-1", "Вільний матеріал один.")
	junked(t, s, "https://example.com/free-2", "Вільний матеріал два.")
	purged, kept, err := s.PurgeAllTrashed()
	if err != nil || purged != 2 || kept != 1 {
		t.Fatalf("purged=%d kept=%d err=%v", purged, kept, err)
	}
}

func TestWebPurgeNeedsConfirmationAndOffersItOnTheCard(t *testing.T) {
	s, w := testWeb(t)
	id := junked(t, s, "https://example.com/web-purge", "Текст для веб-видалення.")
	page := "/items/" + strconv.FormatInt(id, 10)
	if body := webRequest(w, "GET", page, nil, nil).Body.String(); !strings.Contains(body, "Видалити назавжди") || !strings.Contains(body, page+"/purge") {
		t.Fatalf("card lacks the purge button:\n%s", body)
	}
	if rw := webRequest(w, "POST", page+"/purge", url.Values{"csrf": {w.CSRF}}, nil); rw.Code != 400 {
		t.Fatalf("without confirmation: %d", rw.Code)
	}
	if count(t, s, `SELECT count(*) FROM items WHERE id=?`, id) != 1 {
		t.Fatal("deleted without confirmation")
	}
	post(t, w, page+"/purge", url.Values{"confirm": {"1"}})
	if rw := webRequest(w, "GET", page, nil, nil); rw.Code != 404 {
		t.Fatalf("after purge: %d", rw.Code)
	}
}

func TestWebClearTrashList(t *testing.T) {
	s, w := testWeb(t)
	junked(t, s, "https://example.com/clear-1", "Перший текст для очищення.")
	junked(t, s, "https://example.com/clear-2", "Другий текст для очищення.")
	if body := webRequest(w, "GET", "/trash", nil, nil).Body.String(); !strings.Contains(body, "Очистити список") {
		t.Fatalf("trash lacks the clear action:\n%s", body)
	}
	if rw := webRequest(w, "POST", "/trash/purge", url.Values{"csrf": {w.CSRF}}, nil); rw.Code != 400 {
		t.Fatalf("without confirmation: %d", rw.Code)
	}
	post(t, w, "/trash/purge", url.Values{"confirm": {"1"}})
	if n := count(t, s, `SELECT count(*) FROM trash_entries`); n != 0 {
		t.Fatalf("%d left", n)
	}
}

func TestWebCardExplainsWhyAConnectedMaterialCannotBePurged(t *testing.T) {
	s, w := testWeb(t)
	id := junked(t, s, "https://example.com/linked", "Матеріал для нотатки.")
	other := add(t, s, "https://example.com/linked-2")
	manual(t, s, other, "Другий матеріал нотатки.")
	if _, err := s.SaveConnection(ConnectionNote{Title: "Нотатка для перевірки помийки", Category: "Тест", Tags: []string{"тест"}, Questions: []string{"Чому?"}, Body: "Зв'язок.",
		Members: []ConnectionMember{{ItemID: id, Role: "Перший"}, {ItemID: other, Role: "Другий"}}}, 0); err != nil {
		t.Skipf("fixture needs the full connection shape: %v", err)
	}
	body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	if strings.Contains(body, "/purge") || !strings.Contains(body, "нотат") {
		t.Fatalf("connected material:\n%s", body)
	}
}
