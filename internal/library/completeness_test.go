package library

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func selectedSnapshot(t *testing.T, s *Store, id int64) Snapshot {
	t.Helper()
	i, err := s.Item(id)
	if err != nil || !i.Selected.Valid {
		t.Fatal("no selected snapshot", err)
	}
	snap, err := s.Snapshot(i.Selected.Int64)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestConfirmCompletenessOverlaysWithoutChangingTheStoredText(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст, який користувач вважає повним.")
	before := selectedSnapshot(t, s, id)
	if before.Completeness != "unknown" || before.UserConfirmed {
		t.Fatalf("before: %+v", before)
	}
	if err := s.ConfirmCompleteness(before.ID, true, "звірив з оригіналом"); err != nil {
		t.Fatal(err)
	}
	after := selectedSnapshot(t, s, id)
	if after.Completeness != "full" || !after.UserConfirmed || after.ConfirmedAt == "" || !strings.Contains(after.Warning, "підтвердив користувач") {
		t.Fatalf("after: %+v", after)
	}
	if after.Hash != before.Hash || after.Text != before.Text {
		t.Fatal("the immutable text must not change")
	}
	var raw string
	s.DB.QueryRow("SELECT completeness FROM source_versions WHERE id=?", before.ID).Scan(&raw)
	if raw != "unknown" {
		t.Fatalf("extractor's own value must stay: %q", raw)
	}
	if snaps, _ := s.Snapshots(id); len(snaps) != 1 || !snaps[0].UserConfirmed {
		t.Fatalf("Snapshots must show the overlay: %+v", snaps)
	}
}

func TestWithdrawingConfirmationRestoresTheStoredValue(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст.")
	snap := selectedSnapshot(t, s, id)
	s.ConfirmCompleteness(snap.ID, true, "")
	if err := s.ConfirmCompleteness(snap.ID, false, "помилився"); err != nil {
		t.Fatal(err)
	}
	if got := selectedSnapshot(t, s, id); got.Completeness != "unknown" || got.UserConfirmed {
		t.Fatalf("%+v", got)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM completeness_confirmations WHERE snapshot_id=?", snap.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("history must be append-only, got %d rows", n)
	}
}

func TestConfirmCompletenessRejectsUnknownOrTrashedSnapshots(t *testing.T) {
	s := testStore(t)
	if s.ConfirmCompleteness(999, true, "") == nil {
		t.Fatal("unknown snapshot accepted")
	}
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст.")
	snap := selectedSnapshot(t, s, id)
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	if s.ConfirmCompleteness(snap.ID, true, "") == nil {
		t.Fatal("trashed item accepted")
	}
}

func TestWebConfirmCompletenessFlow(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/series")
	manual(t, s, id, strings.Repeat("Вступ до серії. ", 12)+"\n\nExplore more articles")
	snap := selectedSnapshot(t, s, id)
	page := "/items/" + strconv.FormatInt(id, 10)
	body := webRequest(w, "GET", page, nil, nil).Body.String()
	if !strings.Contains(body, "Підтвердити повноту") || !strings.Contains(body, "Текст, схоже, неповний") {
		t.Fatalf("button/diagnosis missing:\n%s", body)
	}
	post(t, w, page+"/completeness", url.Values{"source": {strconv.FormatInt(snap.ID, 10)}, "action": {"confirm"}})
	body = webRequest(w, "GET", page, nil, nil).Body.String()
	if strings.Contains(body, "Текст, схоже, неповний") {
		t.Fatal("a user-confirmed text must not be flagged as probably incomplete")
	}
	if !strings.Contains(body, "Зняти підтвердження") || !strings.Contains(body, "підтвердили ви") {
		t.Fatalf("confirmed state missing:\n%s", body)
	}
	post(t, w, page+"/completeness", url.Values{"source": {strconv.FormatInt(snap.ID, 10)}, "action": {"withdraw"}})
	if body = webRequest(w, "GET", page, nil, nil).Body.String(); !strings.Contains(body, "Підтвердити повноту") {
		t.Fatal("withdrawn confirmation must offer the button again")
	}
}

func TestNoConfirmButtonForAFooterOnlyText(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/footer")
	manual(t, s, id, "Copyright © 2014-2026 The Cucumber Open Source Project")
	if body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String(); strings.Contains(body, "Підтвердити повноту") {
		t.Fatal("confirming completeness of a footer makes no sense")
	}
}

func TestWebConfirmRejectsAForeignSnapshot(t *testing.T) {
	s, w := testWeb(t)
	a, b := add(t, s, "https://example.com/a"), add(t, s, "https://example.com/b")
	manual(t, s, a, "Текст a.")
	manual(t, s, b, "Текст b.")
	other := selectedSnapshot(t, s, b)
	rw := webRequest(w, "POST", "/items/"+strconv.FormatInt(a, 10)+"/completeness",
		url.Values{"csrf": {w.CSRF}, "source": {strconv.FormatInt(other.ID, 10)}, "action": {"confirm"}}, nil)
	if rw.Code != 400 && rw.Code != 404 {
		t.Fatalf("foreign snapshot accepted: %d", rw.Code)
	}
}
