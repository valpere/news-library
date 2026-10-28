package library

import (
	"strconv"
	"strings"
	"testing"
)

func TestDiagnoseSourceExplainsFooterOnlyText(t *testing.T) {
	d := diagnoseSource(Snapshot{ID: 22, RetrievedAt: "2026-10-11T15:33:44Z", Extractor: "readability-v2.1.3",
		Text: "Copyright © 2014-2026 The Cucumber Open Source Project"})
	if d == nil {
		t.Fatal("footer-only text must be diagnosed")
	}
	for _, want := range []string{"#22", "2026-10-11", "«Copyright © 2014-2026 The Cucumber Open Source Project»", "підвал сторінки", "не стаття"} {
		if !strings.Contains(d.Details, want) {
			t.Errorf("details lack %q:\n%s", want, d.Details)
		}
	}
	if !strings.Contains(d.Title, "авторські права") {
		t.Errorf("title %q", d.Title)
	}
}

func TestDiagnoseSourceIgnoresRealText(t *testing.T) {
	for _, text := range []string{"AI agents help defenders detect threats.", strings.Repeat("Звичайна стаття. ", 50)} {
		if d := diagnoseSource(Snapshot{ID: 1, Text: text}); d != nil {
			t.Errorf("%q diagnosed: %+v", text[:20], d)
		}
	}
}

func TestItemPageShowsSourceDiagnosisWithActions(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/index")
	manual(t, s, id, "Copyright © 2014-2026 The Cucumber Open Source Project")
	enableDigestsMin(t, s, 0) // a digest made before the guard existed, like item 24
	j := claim(t, s)
	if err := s.CompleteDigest(j, Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture"}); err != nil {
		t.Fatal(err)
	}
	body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	for _, want := range []string{`id="source-diagnosis"`, "підвал сторінки", "/retry", `href="#manual-text"`, `id="manual-text"`, "/junk", "створено з цього самого тексту"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestItemPageWithoutProblemHasNoDiagnosis(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/ok")
	manual(t, s, id, strings.Repeat("Звичайний текст статті. ", 40))
	if body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String(); strings.Contains(body, `id="source-diagnosis"`) {
		t.Fatal("unexpected diagnosis")
	}
}

// Saved before the extraction hint existed, so the stored warning is the generic
// one; the page must still say what the text itself shows.
func TestDiagnoseSourceFlagsAnIndexPageByItsTrailingMarker(t *testing.T) {
	text := "In our Google Cloud series we share exclusive looks at how Google approaches security topics, straight from Google experts.\n\nExplore more articles"
	d := diagnoseSource(Snapshot{ID: 7, RetrievedAt: "2026-10-11T15:33:29Z", Extractor: "readability-v2.1.3", Completeness: "unknown", Text: text})
	if d == nil || !d.Soft {
		t.Fatalf("a trailing pointer to other articles must give a soft diagnosis: %+v", d)
	}
	for _, want := range []string{"#7", "Explore more articles", "неповний", "не підтверджено"} {
		if !strings.Contains(d.Title+d.Details, want) {
			t.Errorf("lacks %q:\n%s\n%s", want, d.Title, d.Details)
		}
	}
}

func TestDiagnoseSourceStaysQuietForShortCompleteTexts(t *testing.T) {
	cases := []Snapshot{
		{ID: 1, Completeness: "unknown", Text: "Model size used to mean RAM size. colibri breaks that link: it runs a 744B model on a 25GB machine."},
		{ID: 2, Completeness: "full", Text: "Субтитри.\n\nExplore more articles"}, // a trusted full extraction is not second-guessed
		{ID: 3, Completeness: "unknown", Text: strings.Repeat("Довге речення про тему. ", 60) + "останній абзац без крапки і з довжиною більше сорока символів"},
	}
	for _, s := range cases {
		if d := diagnoseSource(s); d != nil {
			t.Errorf("snapshot %d diagnosed: %+v", s.ID, d)
		}
	}
}

func TestSoftDiagnosisDoesNotClaimTheDigestIsUntrustworthy(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/series")
	manual(t, s, id, strings.Repeat("Вступ до серії статей. ", 12)+"\n\nExplore more articles")
	body := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil).Body.String()
	if !strings.Contains(body, `id="source-diagnosis"`) || strings.Contains(body, "не варто довіряти") {
		t.Fatalf("soft diagnosis must be shown without the hard-failure warning")
	}
}
