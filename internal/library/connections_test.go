package library

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
func TestConnectionVersionsBacklinksAndHumanState(t *testing.T) {
	s, n := connectionFixture(t)
	if err := s.SetTags(n.Members[0].ItemID, []string{"existing"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rate(n.Members[0].ItemID, 3, nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveConnection(n, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Connection(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || len(first.Tags) != 2 || !strings.Contains(first.Markdown, "questions:") || !strings.Contains(first.Markdown, "snapshot_id:") {
		t.Fatal(first)
	}
	original, _ := s.Snapshot(first.Members[0].SnapshotID)
	manual(t, s, first.Members[0].ItemID, "A newer source should not change evidence")
	second := first
	second.Body = "Третя стаття уточнює обмеження."
	second.Members = append(second.Members, ConnectionMember{ItemID: 3, Role: "Уточнення"})
	if _, err = s.SaveConnection(second, first.VersionID); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Connection(id, 0)
	historical, _ := s.Connection(id, first.VersionID)
	if current.Revision != 2 || len(current.Members) != 3 || historical.Markdown != first.Markdown || current.Members[0].SnapshotID != original.ID {
		t.Fatal(current, historical)
	}
	for _, m := range current.Members {
		links, e := s.Connections("", m.ItemID, 0)
		if e != nil || len(links) != 1 || links[0].VersionID != current.VersionID {
			t.Fatal(links, e)
		}
	}
	current.Members = current.Members[1:]
	if _, err = s.SaveConnection(current, current.VersionID); err != nil {
		t.Fatal(err)
	}
	links, _ := s.Connections("", first.Members[0].ItemID, 0)
	if len(links) != 0 {
		t.Fatal("stale backlink", links)
	}
	item, _ := s.Item(first.Members[0].ItemID)
	if item.Score.Valid || item.Read || item.Tags != "existing" {
		t.Fatal("tags or read changed, or the rating survived a text change", item)
	}
	notes, e := s.Connections("як відновити", 0, 0)
	if e != nil || len(notes) != 1 {
		t.Fatal(notes, e)
	}
	vocabulary, e := s.ConnectionVocabulary()
	if e != nil || !strings.Contains(strings.Join(vocabulary["tags"], ","), "existing") || len(vocabulary["categories"]) != 1 {
		t.Fatal(vocabulary, e)
	}
}
func TestConnectionValidationOwnershipAndImmutableFiles(t *testing.T) {
	s, n := connectionFixture(t)
	bad := n
	bad.Members = append([]ConnectionMember{}, n.Members...)
	bad.Members[1].ItemID = bad.Members[0].ItemID
	if _, err := s.SaveConnection(bad, 0); err == nil {
		t.Fatal("duplicate accepted")
	}
	item, _ := s.Item(n.Members[0].ItemID)
	bad = n
	bad.Members = append([]ConnectionMember{}, n.Members...)
	bad.Members[1].SnapshotID = item.Selected.Int64
	if _, err := s.SaveConnection(bad, 0); err == nil {
		t.Fatal("foreign source accepted")
	}
	bad = n
	bad.Tags = []string{"bad\ntag"}
	if _, err := s.SaveConnection(bad, 0); err == nil {
		t.Fatal("multiline tag accepted")
	}
	bad = n
	bad.Members = append([]ConnectionMember{}, n.Members...)
	bad.Members[1].DigestID = 9999
	if _, err := s.SaveConnection(bad, 0); err == nil {
		t.Fatal("foreign digest accepted")
	}
	id, err := s.SaveConnection(n, 0)
	if err != nil {
		t.Fatal(err)
	}
	note, _ := s.Connection(id, 0)
	for _, sql := range []string{"UPDATE connection_versions SET title='overwrite'", "DELETE FROM connection_versions", "UPDATE connection_members SET role='overwrite'", "DELETE FROM connection_members"} {
		if _, err = s.DB.Exec(sql); err == nil {
			t.Fatal("immutable record changed", sql)
		}
	}
	var path string
	s.DB.QueryRow("SELECT markdown_path FROM connection_versions WHERE id=?", note.VersionID).Scan(&path)
	if err = os.WriteFile(filepath.Join(s.Files.Root, path), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Connection(id, 0); err == nil {
		t.Fatal("corrupted file accepted")
	}
}
func TestConnectionConcurrentEditsConflict(t *testing.T) {
	s, n := connectionFixture(t)
	id, err := s.SaveConnection(n, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Connection(id, 0)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.SaveConnection(first, first.VersionID); results <- e }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrConnectionConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
func TestConnectionBackupRestoresAllRevisions(t *testing.T) {
	s, n := connectionFixture(t)
	id, err := s.SaveConnection(n, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Connection(id, 0)
	edit := first
	edit.Body = "Updated synthesis"
	if _, err = s.SaveConnection(edit, first.VersionID); err != nil {
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
	old, err := restored.Connection(id, first.VersionID)
	if err != nil || old.Markdown != first.Markdown {
		t.Fatal(old, err)
	}
	latest, err := restored.Connection(id, 0)
	if err != nil || latest.Body != edit.Body || latest.StableID != first.StableID {
		t.Fatal(latest, err)
	}
	links, _ := restored.Connections("", first.Members[0].ItemID, 0)
	if len(links) != 1 {
		t.Fatal(links)
	}
}
func TestConnectionWebDraftConflictAndReadOnlyAPI(t *testing.T) {
	s, n := connectionFixture(t)
	id, err := s.SaveConnection(n, 0)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Connection(id, 0)
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
	form := url.Values{"csrf": {web.CSRF}, "title": {"My draft <script>unsafe()</script>"}, "category": {"SQLite"}, "body": {"Preserve this draft <script>unsafe()</script> [link](javascript:alert(1))"}, "version": {strconv.FormatInt(first.VersionID, 10)}}
	for _, m := range first.Members {
		form.Add("item", strconv.FormatInt(m.ItemID, 10))
		form.Add("source", strconv.FormatInt(m.SnapshotID, 10))
		form.Add("digest", "0")
		form.Add("role", m.Role)
	}
	path := "/connections/" + strconv.FormatInt(id, 10)
	if out := request("POST", path, "Bearer fixture-read-token", form); out.Code != 403 {
		t.Fatal(out.Code)
	}
	if out := request("POST", path, "", form); out.Code != 303 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("POST", path, "", form); out.Code != 409 || !strings.Contains(out.Body.String(), "Preserve this draft") || !strings.Contains(out.Body.String(), `action="`+path+`"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", path, "", nil); out.Code != 200 || (strings.Contains(out.Body.String(), "<script>unsafe()</script>") || strings.Contains(out.Body.String(), `href="javascript:`)) {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := request("GET", path+"/markdown?version="+strconv.FormatInt(first.VersionID, 10), "", nil); out.Code != 200 || out.Body.String() != first.Markdown {
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
