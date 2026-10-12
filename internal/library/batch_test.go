package library

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchURLsPartialDuplicatesAndRestart(t *testing.T) {
	s := backupStore(t)
	existing := add(t, s, "https://example.com/old")
	if err := s.SetRead(existing, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlacement(existing, "deferred"); err != nil {
		t.Fatal(err)
	}
	results, err := s.AddURLs("\n https://example.com/new?utm_source=mail \r\nnot a URL\nhttps://example.com/new\nhttps://example.com/old\nhttps://example.com/second\n")
	if err != nil || len(results) != 5 {
		t.Fatal(results, err)
	}
	expected := []string{"added", "failed", "existing", "existing", "added"}
	for i, result := range results {
		if result.Status != expected[i] || result.Line != i+2 {
			t.Fatal(results)
		}
	}
	if results[0].ItemID != results[2].ItemID || results[3].ItemID != existing {
		t.Fatal(results)
	}
	path := filepath.Join(s.Files.Root, "library.sqlite")
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var items, jobs int
	reopened.DB.QueryRow("SELECT count(*) FROM items").Scan(&items)
	reopened.DB.QueryRow("SELECT count(*) FROM jobs WHERE stage='fetch' AND state='queued'").Scan(&jobs)
	if items != 3 || jobs != 3 {
		t.Fatal(items, jobs)
	}
	old, _ := reopened.Item(existing)
	fresh, _ := reopened.Item(results[0].ItemID)
	if !old.Read || old.Placement != "deferred" || fresh.Read || fresh.Score.Valid || fresh.Placement != "inbox" {
		t.Fatal(old, fresh)
	}
	if _, err = reopened.AddURLs("https://example.com/new\nhttps://example.com/second"); err != nil {
		t.Fatal(err)
	}
	reopened.DB.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs)
	if jobs != 3 {
		t.Fatal(jobs)
	}
}
func TestBatchBoundsAndIsolatedStorageFailure(t *testing.T) {
	s := backupStore(t)
	for _, raw := range []string{" \r\n ", strings.Repeat("https://example.com/a\n", BatchURLLimit+1), strings.Repeat("x", BatchInputLimit+1), string([]byte{0xff})} {
		if _, err := s.AddURLs(raw); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM items").Scan(&count)
	if count != 0 {
		t.Fatal("partial oversized batch", count)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fixture_batch_failure BEFORE INSERT ON items WHEN NEW.canonical_url='https://example.com/fail' BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	results, err := s.AddURLs("https://example.com/first\nhttps://example.com/fail\nhttps://example.com/last")
	if err != nil || len(results) != 3 || results[0].Status != "added" || results[1].Status != "failed" || results[2].Status != "added" || results[1].ItemID != 0 {
		t.Fatal(results, err)
	}
	s.DB.QueryRow("SELECT count(*) FROM items").Scan(&count)
	if count != 2 {
		t.Fatal(count)
	}
}
func TestBatchWebReportCorrectionAndMutationGuards(t *testing.T) {
	s := backupStore(t)
	web, err := NewWeb(s, "http://192.0.2.1:8787", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.Handler()
	request := func(auth, origin, csrf string) *httptest.ResponseRecorder {
		form := url.Values{"csrf": {csrf}, "urls": {"https://example.com/first\n<script>unsafe()</script>\nhttps://example.com/second"}}
		r := httptest.NewRequest("POST", "http://192.0.2.1:8787/items/batch", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	for _, values := range [][3]string{{"Bearer agent", "", web.CSRF}, {"", "http://elsewhere", web.CSRF}, {"", "", "wrong"}} {
		if out := request(values[0], values[1], values[2]); out.Code != 403 {
			t.Fatal(out.Code)
		}
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM items").Scan(&count)
	if count != 0 {
		t.Fatal("guard allowed writes", count)
	}
	out := request("", "http://192.0.2.1:8787", web.CSRF)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Нових: 2") || !strings.Contains(out.Body.String(), "Не додано: 1") || strings.Contains(out.Body.String(), "<script>unsafe()</script>") {
		t.Fatal(out.Code, out.Body.String())
	}
	// Correction form must retain only the failed line, never resubmit accepted URLs.
	start := strings.Index(out.Body.String(), `<textarea id="batch-urls"`)
	if start < 0 {
		t.Fatal("missing correction form")
	}
	// Find the actual tag boundary; placeholder contains a sample URL.
	start += strings.Index(out.Body.String()[start:], ">") + 1
	end := strings.Index(out.Body.String()[start:], "</textarea>") + start
	if end < start || strings.Contains(out.Body.String()[start:end], "https://example.com/") {
		t.Fatal("accepted URL in correction form")
	}

	if again := request("", "", web.CSRF); again.Code != 200 || !strings.Contains(again.Body.String(), "Уже були: 2") {
		t.Fatal(again.Code, again.Body.String())
	}
}
