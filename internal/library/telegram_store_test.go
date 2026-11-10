package library

import (
	"reflect"
	"testing"
)

func TestAddFromTelegramReportsAddedExistingAndTrashed(t *testing.T) {
	s := testStore(t)
	id, status, err := s.AddFromTelegram("https://a.example/1?utm_source=x")
	if err != nil || status != TelegramAdded || id == 0 {
		t.Fatalf("%d %q %v", id, status, err)
	}
	if got := scalar[string](t, s, `SELECT telegram FROM items WHERE id=?`, id); got != "pending" {
		t.Fatalf("telegram flag %q", got)
	}
	again, status, err := s.AddFromTelegram("https://a.example/1")
	if err != nil || status != TelegramExists || again != id {
		t.Fatalf("%d %q %v", again, status, err)
	}
	web := add(t, s, "https://a.example/web")
	if _, status, _ = s.AddFromTelegram("https://a.example/web"); status != TelegramExists {
		t.Fatalf("status %q", status)
	}
	if got := scalar[string](t, s, `SELECT telegram FROM items WHERE id=?`, web); got != "" {
		t.Fatalf("an item added in the web UI is not delivered to Telegram, flag %q", got)
	}
	if err = s.MarkJunk(id, ""); err != nil {
		t.Fatal(err)
	}
	if got, status, _ := s.AddFromTelegram("https://a.example/1"); status != TelegramTrashed || got != id {
		t.Fatalf("%d %q", got, status)
	}
	if _, _, err = s.AddFromTelegram("not a url"); err == nil {
		t.Fatal("invalid URL")
	}
}

func TestTelegramPendingWaitsForProcessingAndSendsOnce(t *testing.T) {
	s := testStore(t)
	a, _, _ := s.AddFromTelegram("https://a.example/1")
	b, _, _ := s.AddFromTelegram("https://a.example/2")
	c, _, _ := s.AddFromTelegram("https://a.example/3")
	web := add(t, s, "https://a.example/web")
	for id, state := range map[int64]string{a: "ready", b: "queued", c: "needs_input", web: "ready"} {
		if _, err := s.DB.Exec(`UPDATE items SET state=? WHERE id=?`, state, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.TelegramPending()
	if err != nil || !reflect.DeepEqual(got, []int64{a, c}) {
		t.Fatalf("pending %v %v", got, err)
	}
	if err = s.MarkTelegramSent(a); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.TelegramPending(); !reflect.DeepEqual(got, []int64{c}) {
		t.Fatalf("pending after send %v", got)
	}
	// A replaced text recalculates the digest but never makes the item pending again.
	manual(t, s, a, "new text of the first article")
	if flag := scalar[string](t, s, `SELECT telegram FROM items WHERE id=?`, a); flag != "sent" {
		t.Fatalf("flag %q", flag)
	}
}
