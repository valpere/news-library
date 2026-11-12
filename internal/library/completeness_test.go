package library

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func itemText(t *testing.T, s *Store, id int64) SourceText {
	t.Helper()
	i, err := s.Item(id)
	if err != nil || !i.HasText {
		t.Fatal("no selected snapshot", err)
	}
	snap, err := s.SourceText(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestConfirmCompletenessOverlaysWithoutChangingTheStoredText(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст, який користувач вважає повним.")
	before := itemText(t, s, id)
	if before.Completeness != "unknown" || before.UserConfirmed {
		t.Fatalf("before: %+v", before)
	}
	if err := s.ConfirmCompleteness(before.ItemID, true); err != nil {
		t.Fatal(err)
	}
	after := itemText(t, s, id)
	if after.Completeness != "full" || !after.UserConfirmed || !strings.Contains(after.Warning, "підтвердив користувач") {
		t.Fatalf("after: %+v", after)
	}
	if after.Hash != before.Hash || after.Text != before.Text {
		t.Fatal("the immutable text must not change")
	}
	var raw string
	s.DB.QueryRow("SELECT completeness FROM texts WHERE item_id=?", before.ItemID).Scan(&raw)
	if raw != "unknown" {
		t.Fatalf("extractor's own value must stay: %q", raw)
	}
	if snaps, _ := s.Texts(id); len(snaps) != 1 || !snaps[0].UserConfirmed {
		t.Fatalf("Texts must show the overlay: %+v", snaps)
	}
}

func TestWithdrawingConfirmationRestoresTheStoredValue(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст.")
	snap := itemText(t, s, id)
	s.ConfirmCompleteness(snap.ItemID, true)
	if err := s.ConfirmCompleteness(snap.ItemID, false); err != nil {
		t.Fatal(err)
	}
	if got := itemText(t, s, id); got.Completeness != "unknown" || got.UserConfirmed {
		t.Fatalf("%+v", got)
	}
}

func TestConfirmCompletenessRejectsUnknownOrTrashedSnapshots(t *testing.T) {
	s := testStore(t)
	if s.ConfirmCompleteness(999, true) == nil {
		t.Fatal("unknown snapshot accepted")
	}
	id := add(t, s, "https://example.com/a")
	manual(t, s, id, "Текст.")
	snap := itemText(t, s, id)
	if err := s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	if s.ConfirmCompleteness(snap.ItemID, true) == nil {
		t.Fatal("trashed item accepted")
	}
}

func TestWebConfirmCompletenessFlow(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/series")
	manual(t, s, id, strings.Repeat("Вступ до серії. ", 12)+"\n\nExplore more articles")
	snap := itemText(t, s, id)
	page := "/items/" + strconv.FormatInt(id, 10)
	body := webRequest(w, "GET", page, nil, nil).Body.String()
	if !strings.Contains(body, "Підтвердити повноту") || !strings.Contains(body, "Можливо, текст неповний") {
		t.Fatalf("button/diagnosis missing:\n%s", body)
	}
	post(t, w, page+"/completeness", url.Values{"source": {strconv.FormatInt(snap.ItemID, 10)}, "action": {"confirm"}})
	body = webRequest(w, "GET", page, nil, nil).Body.String()
	if strings.Contains(body, "Можливо, текст неповний") {
		t.Fatal("a user-confirmed text must not be flagged as probably incomplete")
	}
	if strings.Contains(body, "Зняти підтвердження") || !strings.Contains(body, "підтвердили ви") {
		t.Fatalf("confirmed state missing:\n%s", body)
	}
	post(t, w, page+"/completeness", url.Values{"source": {strconv.FormatInt(snap.ItemID, 10)}, "action": {"withdraw"}})
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

func TestWebSoftSignsStayQuietInsideSavedText(t *testing.T) {
	s, w := testWeb(t)
	cut := strings.Repeat("Вступ до статті. ", 12) + "\n\nАвтор пояснює, що результати експерименту залежать від багатьох чинників, серед яких і"
	teaser := strings.Repeat("Вступ до серії. ", 12) + "\n\nExplore more articles"
	for name, text := range map[string]string{"cut": cut, "teaser": teaser} {
		id := add(t, s, "https://example.com/"+name)
		manual(t, s, id, text)
		body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
		for _, bad := range []string{`id="source-diagnosis"`, "заглушка", "Дотягнути решту тексту", "· unknown ·"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: soft sign must stay quiet, found %q", name, bad)
			}
		}
		i := strings.Index(body, "Повний збережений текст")
		if i < 0 || !strings.Contains(body[i:], "Можливо, текст неповний") || !strings.Contains(body[i:], "Підтвердити повноту") {
			t.Errorf("%s: the quiet note and confirm button belong inside the saved-text details", name)
		}
	}
}

func TestSameTextRefetchKeepsConfirmationChangedTextClearsIt(t *testing.T) {
	s := testStore(t)
	id := add(t, s, "https://example.com/refetch")
	if err := s.Complete(claim(t, s), SourceText{Title: "T", Text: "Same text", Origin: "fetched", Extractor: "fixture", Completeness: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmCompleteness(id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(claim(t, s), SourceText{Title: "T", Text: "Same text", Origin: "fetched", Extractor: "fixture", Completeness: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if got := itemText(t, s, id); !got.UserConfirmed {
		t.Fatal("the same text lost its confirmation")
	}
	manual(t, s, id, "Changed text")
	if got := itemText(t, s, id); got.UserConfirmed {
		t.Fatal("changed text kept the confirmation")
	}
}

func TestPageNeverPrintsRawUnknownCompleteness(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/plain")
	manual(t, s, id, strings.Repeat("Звичайний завершений абзац. ", 20)+"Кінець.")
	body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	for _, bad := range []string{"· unknown", "unknown ·", ">unknown<"} {
		if strings.Contains(body, bad) {
			t.Fatalf("raw completeness value leaked: %q", bad)
		}
	}
}
