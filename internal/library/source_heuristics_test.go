package library

import (
	"net/url"
	"strings"
	"testing"
)

func TestPartialSourceSuspicion(t *testing.T) {
	long := strings.Repeat("Це довге речення про тему статті. ", 60)
	cases := []struct {
		name, text string
		want       bool
	}{
		{"complete article", long, false},
		{"too short", "Gherkin is a language.", true},
		{"cut mid sentence", long + "і потім автор пояснює, що", true},
		{"ends with closing quote", long + "«кінець»", false},
		{"ends with code block", long + "\nfmt.Println(x)", false},
		{"read more tail", long + "\nContinue reading on our site", true},
		{"ukrainian read more", long + "\nЧитати далі", true},
		{"paywall", long + "\nSubscribe to continue reading", true},
	}
	for _, c := range cases {
		if got := partialSourceSuspicion(c.text) != ""; got != c.want {
			t.Errorf("%s: suspicion=%v want %v", c.name, got, c.want)
		}
	}
}

func TestDecodeArticleWarnsOnSuspectedPartial(t *testing.T) {
	html := []byte("<html><head><title>Gherkin</title></head><body><article><p>Gherkin is a language.</p></article></body></html>")
	u, _ := url.Parse("https://example.com/g")
	snap, err := decodeArticle(html, u, "text/html", false, &NetworkMeta{FetchedAt: nowUTC()})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != "unknown" || !strings.Contains(snap.Warning, "Підозра на неповний витяг") {
		t.Fatalf("%q %q", snap.Completeness, snap.Warning)
	}
}
