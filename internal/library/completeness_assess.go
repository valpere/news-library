package library

// Completeness is the one answer to "how complete is this saved text": the
// extractor's status, what the text itself suggests, and the user's verdict.
// Pages, digests, Telegram and the agent API read it instead of combining
// Snapshot.Completeness with their own heuristics.
type Completeness struct {
	// Status is the extractor's claim (full, partial or unknown) with the user's
	// confirmation overlaid; unknown means "no claim", not "probably incomplete".
	Status string
	// Reasons are the signs in the text that it may be cut off.
	Reasons []string
	// Diagnosis is set when the text is not an article or looks cut off.
	Diagnosis *SourceDiagnosis
	Confirmed bool
	// CanConfirm: the text looks incomplete and the user can vouch for it. A text
	// known to be truncated is re-fetched instead, never confirmed.
	CanConfirm bool
}

// incompleteSigns lists the signs in text that it is a teaser or an index rather
// than the whole article. It is the only place that judges this from content.
func incompleteSigns(text string) []string {
	if marker := trailingMarker(text); marker != "" {
		return []string{marker}
	}
	return nil
}

func Assess(s Snapshot) Completeness {
	a := Completeness{Status: s.Completeness, Confirmed: s.UserConfirmed, Diagnosis: diagnoseSource(s)}
	if a.Diagnosis != nil && a.Diagnosis.Soft {
		a.Reasons = incompleteSigns(s.Text)
		a.CanConfirm = !s.UserConfirmed && s.Completeness != "partial"
	}
	return a
}
