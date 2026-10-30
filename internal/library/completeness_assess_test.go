package library

import (
	"testing"
)

func TestAssessOffersConfirmationOnlyWhenTextLooksIncomplete(t *testing.T) {
	long := "Перший абзац статті.\nДругий абзац статті."
	cases := []struct {
		name        string
		s           SourceText
		wantConfirm bool
		wantReasons int
	}{
		{"plain html text without signs", SourceText{Text: long, Completeness: "unknown"}, false, 0},
		{"teaser marker", SourceText{Text: long + "\nRead more", Completeness: "unknown"}, true, 1},
		{"already confirmed", SourceText{Text: long + "\nRead more", Completeness: "full", UserConfirmed: true}, false, 0},
		{"footer only is hard, not confirmable", SourceText{Text: "© 2026 Acme Inc.", Completeness: "unknown"}, false, 0},
		{"extractor guarantees full", SourceText{Text: long + "\nRead more", Completeness: "full"}, false, 0},
		{"known truncated is re-fetched, not confirmed", SourceText{Text: long, Completeness: "partial"}, false, 0},
	}
	for _, c := range cases {
		a := Assess(c.s)
		if a.CanConfirm != c.wantConfirm || len(a.Reasons) != c.wantReasons {
			t.Errorf("%s: CanConfirm=%v reasons=%v", c.name, a.CanConfirm, a.Reasons)
		}
	}
}

func TestAssessKeepsStatusAndConfirmation(t *testing.T) {
	a := Assess(SourceText{Text: "один\nдва", Completeness: "full", UserConfirmed: true})
	if a.Status != "full" || !a.Confirmed || a.Diagnosis != nil {
		t.Fatalf("%+v", a)
	}
}

func TestCompletenessLabel(t *testing.T) {
	body := "Перший абзац.\nДругий абзац."
	cases := []struct {
		name string
		s    SourceText
		want string
	}{
		{"full", SourceText{Text: body, Completeness: "full"}, "повний текст"},
		{"partial", SourceText{Text: body, Completeness: "partial"}, "частковий текст"},
		{"unknown without signs says nothing", SourceText{Text: body, Completeness: "unknown"}, ""},
		{"teaser marker", SourceText{Text: body + "\nRead more", Completeness: "unknown"}, "схоже на неповний текст («Read more»)"},
	}
	for _, c := range cases {
		if got := Assess(c.s).Label(); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
}

func TestAssessDistinguishesTeaserFromCutoff(t *testing.T) {
	head := "Перший абзац статті про одну тему.\n"
	cut := "Автор пояснює, що результати експерименту залежать від багатьох чинників, серед яких і"
	cases := []struct {
		name     string
		s        SourceText
		wantKind string
	}{
		{"teaser", SourceText{Text: head + "Read more", Completeness: "unknown"}, "teaser"},
		{"cut mid sentence", SourceText{Text: head + cut, Completeness: "unknown"}, "cutoff"},
		{"short list item is not a cutoff", SourceText{Text: head + "CLAUDE.md: store project conventions", Completeness: "unknown"}, ""},
		{"finished sentence", SourceText{Text: head + cut + " погода.", Completeness: "unknown"}, ""},
		{"subtitles are guaranteed full", SourceText{Text: head + cut, Completeness: "full"}, ""},
	}
	for _, c := range cases {
		a := Assess(c.s)
		got := ""
		if len(a.Reasons) > 0 {
			got = a.Reasons[0].Kind
		}
		if got != c.wantKind {
			t.Errorf("%s: kind %q want %q (%+v)", c.name, got, c.wantKind, a.Reasons)
		}
		if c.wantKind != "" && (a.Diagnosis == nil || a.Diagnosis.Kind != c.wantKind || !a.CanConfirm) {
			t.Errorf("%s: diagnosis %+v confirm %v", c.name, a.Diagnosis, a.CanConfirm)
		}
	}
	if l := Assess(SourceText{Text: head + cut, Completeness: "unknown"}).Label(); l != "текст обривається посеред речення" {
		t.Errorf("label %q", l)
	}
}

func TestShownWarningDropsTheOldJudgmentSuffix(t *testing.T) {
	cases := map[string]string{
		"Виділений текст HTML; повноту оригіналу не підтверджено":                                                  "Виділений текст HTML",
		"Повний текст статті з читалки Bytez (5877 слів); повноту оригіналу не підтверджено":                       "Повний текст статті з читалки Bytez (5877 слів)",
		"Повноту підтвердив користувач 2026-10-28; витяг: Виділений текст HTML; повноту оригіналу не підтверджено": "Повноту підтвердив користувач 2026-10-28; витяг: Виділений текст HTML",
		"Виділений текст HTML": "Виділений текст HTML",
		"Субтитри: en":         "Субтитри: en",
	}
	for in, want := range cases {
		if got := (SourceText{Warning: in}).ShownWarning(); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}
