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
