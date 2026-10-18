package library

import (
	"strconv"
	"strings"
	"testing"
)

// The digest (or its processing state) is the primary content of an item page;
// knowledge, rule history and versions are secondary and must follow it.
func TestDetailPageShowsDigestBeforeSecondarySections(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/layout")
	if err := s.Manual(id, "Короткий текст для перевірки порядку блоків на сторінці."); err != nil {
		t.Fatal(err)
	}
	rw := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil)
	body := rw.Body.String()
	main := strings.Index(body, `id="processing"`)
	if main < 0 {
		t.Fatal("processing section missing")
	}
	for _, secondary := range []string{`id="knowledge"`, `id="connection-backlinks"`} {
		if at := strings.Index(body, secondary); at < main {
			t.Errorf("%s (%d) must come after the main section (%d)", secondary, at, main)
		}
	}
}
