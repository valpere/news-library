package library

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Completeness is the one answer to "how complete is this saved text": the
// extractor's status, what the text itself suggests, and the user's verdict.
// Pages, digests, Telegram and the agent API read it instead of combining
// Snapshot.Completeness with their own heuristics.
type Completeness struct {
	// Status is the extractor's claim (full, partial or unknown) with the user's
	// confirmation overlaid; unknown means "no claim", not "probably incomplete".
	Status string
	// Reasons are the signs in the text that it may be cut off, most telling first.
	Reasons []Reason
	// Diagnosis is set when the text is not an article or looks cut off.
	Diagnosis *SourceDiagnosis
	Confirmed bool
	// CanConfirm: the text looks incomplete and the user can vouch for it. A text
	// known to be truncated is re-fetched instead, never confirmed.
	CanConfirm bool
}

// Reason is one sign that a text is not the whole article. Kind decides what the
// user is offered: a "teaser" (the text ends with a pointer to other pages) is
// probably an index or intro, so keeping it at all is questionable; a "cutoff"
// (a long last line stops mid-sentence) is probably an article worth recovering.
type Reason struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

const (
	reasonTeaser = "teaser"
	reasonCutoff = "cutoff"
	// A last line shorter than this is usually a heading, list item, link or code,
	// so stopping without punctuation there says nothing.
	cutoffMinRunes = 80
)

// incompleteSigns lists the signs in text that it is a teaser, an index or a cut
// off article. It is the only place that judges this from content.
func incompleteSigns(text string) []Reason {
	signs := []Reason{}
	if marker := trailingMarker(text); marker != "" {
		signs = append(signs, Reason{reasonTeaser, marker})
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) >= 2 && utf8.RuneCountInString(last) >= cutoffMinRunes {
		if r, _ := utf8.DecodeLastRuneInString(last); unicode.IsLetter(r) || unicode.IsDigit(r) || r == ',' {
			signs = append(signs, Reason{reasonCutoff, last})
		}
	}
	return signs
}

func Assess(s Snapshot) Completeness {
	a := Completeness{Status: s.Completeness, Reasons: []Reason{}, Confirmed: s.UserConfirmed, Diagnosis: diagnoseSource(s)}
	if a.Diagnosis != nil && a.Diagnosis.Soft {
		a.Reasons = incompleteSigns(s.Text)
		a.CanConfirm = !s.UserConfirmed && s.Completeness != "partial"
	}
	return a
}

// Label is the short human wording for readers of a summary (Telegram, exports).
// An unknown status without signs says nothing: that is the ordinary HTML case,
// not a warning.
func (c Completeness) Label() string {
	switch {
	case len(c.Reasons) > 0 && c.Reasons[0].Kind == reasonTeaser:
		return "схоже на неповний текст («" + c.Reasons[0].Detail + "»)"
	case len(c.Reasons) > 0:
		return "текст обривається посеред речення"
	case c.Status == "full":
		return "повний текст"
	case c.Status == "partial":
		return "частковий текст"
	}
	return ""
}

// ShownWarning is the warning as readers see it. Texts saved before extractors
// stopped judging completeness carry "; повноту оригіналу не підтверджено" in the
// stored warning; versions are immutable, so it is dropped at display time.
func (s Snapshot) ShownWarning() string {
	return strings.TrimSuffix(s.Warning, "; повноту оригіналу не підтверджено")
}
