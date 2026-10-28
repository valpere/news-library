package library

import "testing"

func TestAssessOffersConfirmationOnlyWhenTextLooksIncomplete(t *testing.T) {
	long := "Перший абзац статті.\nДругий абзац статті."
	cases := []struct {
		name        string
		s           Snapshot
		wantConfirm bool
		wantReasons int
	}{
		{"plain html text without signs", Snapshot{Text: long, Completeness: "unknown"}, false, 0},
		{"teaser marker", Snapshot{Text: long + "\nRead more", Completeness: "unknown"}, true, 1},
		{"already confirmed", Snapshot{Text: long + "\nRead more", Completeness: "full", UserConfirmed: true}, false, 0},
		{"footer only is hard, not confirmable", Snapshot{Text: "© 2026 Acme Inc.", Completeness: "unknown"}, false, 0},
		{"extractor guarantees full", Snapshot{Text: long + "\nRead more", Completeness: "full"}, false, 0},
		{"known truncated is re-fetched, not confirmed", Snapshot{Text: long, Completeness: "partial"}, false, 0},
	}
	for _, c := range cases {
		a := Assess(c.s)
		if a.CanConfirm != c.wantConfirm || len(a.Reasons) != c.wantReasons {
			t.Errorf("%s: CanConfirm=%v reasons=%v", c.name, a.CanConfirm, a.Reasons)
		}
	}
}

func TestAssessKeepsStatusAndConfirmation(t *testing.T) {
	a := Assess(Snapshot{Text: "один\nдва", Completeness: "full", UserConfirmed: true})
	if a.Status != "full" || !a.Confirmed || a.Diagnosis != nil {
		t.Fatalf("%+v", a)
	}
}
