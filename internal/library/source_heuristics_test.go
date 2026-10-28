package library

import (
	"net/url"
	"testing"
)

func TestDecodeArticleMakesNoCompletenessJudgment(t *testing.T) {
	html := []byte("<html><head><title>Gherkin</title></head><body><article><p>Gherkin is a language.</p></article></body></html>")
	u, _ := url.Parse("https://example.com/g")
	snap, err := decodeArticle(html, u, "text/html", false, &NetworkMeta{FetchedAt: nowUTC()})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != "unknown" || snap.Warning != "Виділений текст HTML" {
		t.Fatalf("%q %q", snap.Completeness, snap.Warning)
	}
}
