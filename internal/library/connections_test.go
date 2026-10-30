package library

import (
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func connectionFixture(t *testing.T) (*Store, ConnectionNote) {
	t.Helper()
	s := backupStore(t)
	n := ConnectionNote{Title: "Узгоджене відновлення SQLite", Category: "Зберігання даних", Tags: []string{"sqlite", "backup", "SQLite"}, Questions: []string{"Як відновити базу без втрат?"}, Body: "Дві статті пояснюють узгодженість з різних боків."}
	for _, name := range []string{"wal", "snapshot", "third"} {
		id := add(t, s, "https://example.com/"+name)
		manual(t, s, id, "Source "+name)
		n.Members = append(n.Members, ConnectionMember{ItemID: id, Role: "Пояснює " + name})
	}
	n.Members = n.Members[:2]
	return s, n
}
func TestConnectionIsReplacedNotVersioned(t *testing.T) {
	s, n := connectionFixture(t)
	if err := s.SetTags(n.Members[0].ItemID, []string{"existing"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rate(n.Members[0].ItemID, 3, nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveConnection(n)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Connection(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tags) != 2 || !strings.Contains(first.Markdown, "questions:") || !strings.Contains(first.Markdown, "item_id:") {
		t.Fatal(first)
	}
	var oldPath string
	s.DB.QueryRow("SELECT markdown_path FROM connection_notes WHERE id=?", id).Scan(&oldPath)
	second := first
	second.Body = "Третя стаття уточнює обмеження."
	second.Members = append(second.Members, ConnectionMember{ItemID: 3, Role: "Уточнення"})
	if _, err = s.SaveConnection(second); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Connection(id)
	if len(current.Members) != 3 || current.Body != second.Body || count(t, s, "SELECT count(*) FROM connection_notes") != 1 {
		t.Fatal(current)
	}
	if _, err = os.Stat(filepath.Join(s.Files.Root, oldPath)); !os.IsNotExist(err) {
		t.Fatal("file of the replaced note kept", err)
	}
	for _, m := range current.Members {
		links, e := s.Connections("", m.ItemID)
		if e != nil || len(links) != 1 || links[0].ID != id {
			t.Fatal(links, e)
		}
	}
	current.Members = current.Members[1:]
	if _, err = s.SaveConnection(current); err != nil {
		t.Fatal(err)
	}
	if links, _ := s.Connections("", first.Members[0].ItemID); len(links) != 0 {
		t.Fatal("stale backlink", links)
	}
	notes, e := s.Connections("як відновити", 0)
	if e != nil || len(notes) != 1 {
		t.Fatal(notes, e)
	}
	vocabulary, e := s.ConnectionVocabulary()
	if e != nil || !strings.Contains(strings.Join(vocabulary["tags"], ","), "existing") || len(vocabulary["categories"]) != 1 {
		t.Fatal(vocabulary, e)
	}
}
func TestChangedTextTakesItsMaterialOutOfNotes(t *testing.T) {
	s, n := connectionFixture(t)
	third := add(t, s, "https://example.com/fourth")
	manual(t, s, third, "Fourth text")
	n.Members = append(n.Members, ConnectionMember{ItemID: third, Role: "Четвертий"})
	id, err := s.SaveConnection(n)
	if err != nil {
		t.Fatal(err)
	}
	manual(t, s, third, "Fourth text, rewritten")
	note, err := s.Connection(id)
	if err != nil || len(note.Members) != 2 || strings.Contains(note.Markdown, "Четвертий") {
		t.Fatal("link of the old text survived", note, err)
	}
	manual(t, s, n.Members[0].ItemID, "First text, rewritten")
	if _, err = s.Connection(id); err == nil {
		t.Fatal("a note with one material left must be deleted")
	}
}
func TestConnectionValidationAndFileIntegrity(t *testing.T) {
	s, n := connectionFixture(t)
	bad := n
	bad.Members = append([]ConnectionMember{}, n.Members...)
	bad.Members[1].ItemID = bad.Members[0].ItemID
	if _, err := s.SaveConnection(bad); err == nil {
		t.Fatal("duplicate accepted")
	}
	bad = n
	bad.Members = append([]ConnectionMember{}, n.Members...)
	bad.Members[1].ItemID = 9999
	if _, err := s.SaveConnection(bad); err == nil {
		t.Fatal("unknown material accepted")
	}
	bad = n
	bad.Tags = []string{"bad\ntag"}
	if _, err := s.SaveConnection(bad); err == nil {
		t.Fatal("multiline tag accepted")
	}
	id, err := s.SaveConnection(n)
	if err != nil {
		t.Fatal(err)
	}
	var path string
	s.DB.QueryRow("SELECT markdown_path FROM connection_notes WHERE id=?", id).Scan(&path)
	if err = os.WriteFile(filepath.Join(s.Files.Root, path), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Connection(id); err == nil {
		t.Fatal("corrupted file accepted")
	}
}
func TestConnectionBackupRestoresTheCurrentNote(t *testing.T) {
	s, n := connectionFixture(t)
	id, err := s.SaveConnection(n)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Connection(id)
	edit := first
	edit.Body = "Updated synthesis"
	if _, err = s.SaveConnection(edit); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	restore := filepath.Join(root, "restore")
	if _, err = Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err = Restore(context.Background(), backup, restore); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(restore, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	latest, err := restored.Connection(id)
	if err != nil || latest.Body != edit.Body {
		t.Fatal(latest, err)
	}
	links, _ := restored.Connections("", first.Members[0].ItemID)
	if len(links) != 1 {
		t.Fatal(links)
	}
}
func TestConnectionWebLastSaveWinsAndReadOnlyAPI(t *testing.T) {
	s, n := connectionFixture(t)
	id, err := s.SaveConnection(n)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Connection(id)
	web, err := NewWeb(s, "http://127.0.0.1:8787", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	web.ReadToken = "fixture-read-token"
	handler := web.Handler()
	request := func(method, path, auth string, form url.Values) *httptest.ResponseRecorder {
		body := ""
		if form != nil {
			body = form.Encode()
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, r)
		return out
	}
	form := url.Values{"csrf": {web.CSRF}, "title": {"My draft <script>unsafe()</script>"}, "category": {"SQLite"}, "body": {"Preserve this draft <script>unsafe()</script> [link](javascript:alert(1))"}}
	for _, m := range first.Members {
		form.Add("item", strconv.FormatInt(m.ItemID, 10))
		form.Add("role", m.Role)
	}
	path := "/connections/" + strconv.FormatInt(id, 10)
	if out := request("POST", path, "Bearer fixture-read-token", form); out.Code != 403 {
		t.Fatal(out.Code)
	}
	for range 2 {
		if out := request("POST", path, "", form); out.Code != 303 {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	bad := url.Values{"csrf": {web.CSRF}, "title": {"Draft kept on error"}, "body": {"Body kept"}, "item": {strconv.FormatInt(first.Members[0].ItemID, 10)}, "role": {"one"}}
	if out := request("POST", path, "", bad); out.Code != 400 || !strings.Contains(out.Body.String(), "Draft kept on error") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", path, "", nil); out.Code != 200 || (strings.Contains(out.Body.String(), "<script>unsafe()</script>") || strings.Contains(out.Body.String(), `href="javascript:`)) {
		t.Fatal(out.Code, out.Body.String())
	}
	current, _ := s.Connection(id)
	if out := request("GET", path+"/markdown", "", nil); out.Code != 200 || out.Body.String() != current.Markdown {
		t.Fatal(out.Code)
	}
	if out := request("GET", "/api/read/connections", "", nil); out.Code != 401 {
		t.Fatal(out.Code)
	}
	for _, apiPath := range []string{"/api/read/connections", "/api/read" + path, "/api/read/items/1"} {
		if out := request("GET", apiPath, "Bearer fixture-read-token", nil); out.Code != 200 || !strings.Contains(out.Body.String(), "My draft") {
			t.Fatal(apiPath, out.Code, out.Body.String())
		}
	}
}
