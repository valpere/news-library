package library

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// One notice, not a minimum source length. Additional substantive lines pass.
var copyrightNotice = regexp.MustCompile(`(?i)^(?:copyright(?:\s+(?:©|\(c\)))?|©|\(c\))\s*(?:19|20)\d{2}(?:\s*[-–—]\s*(?:19|20)\d{2})?\s+[\p{L}\p{N} &'’_,()-]{1,160}\.?$`)

func copyrightOnly(text string) bool {
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(strings.TrimSuffix(line, "."), "all rights reserved") {
			continue
		}
		if len(lines) > 0 {
			return false
		}
		// A suffix on the copyright line is common too.
		lower := strings.ToLower(line)
		if i := strings.LastIndex(lower, "all rights reserved"); i >= 0 && strings.TrimSpace(strings.TrimSuffix(lower[i:], ".")) == "all rights reserved" {
			line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[:i]), "."))
		}
		lines = append(lines, line)
	}
	return len(lines) == 1 && copyrightNotice.MatchString(lines[0])
}

// Recognize the served interstitial, not articles discussing browser challenges.
func ValidateSourceContent(s Snapshot) error {
	title := strings.ToLower(strings.TrimSpace(s.Title))
	text := strings.ToLower(strings.ReplaceAll(s.Text, "’", "'"))
	blocked := (title == "client challenge" && strings.Contains(text, "a required part of this site couldn't load")) ||
		(title == "just a moment..." && (strings.Contains(text, "verify you are human") || strings.Contains(text, "enable javascript and cookies")))
	if blocked {
		return &SourceError{Kind: "source", Challenge: true, Message: "замість статті джерело повернуло перевірку браузера; потрібне повторне отримання тексту, а не повтор дайджесту"}
	}
	if copyrightOnly(s.Text) {
		return &SourceError{Kind: "source", FooterOnly: true, Message: "у збереженому тексті є лише рядок про авторські права (copyright), статті немає; причину й що робити описано на сторінці матеріалу, повтор дайджесту не допоможе"}
	}
	return nil
}

// SourceDiagnosis explains, in plain words, why a saved text cannot yield a
// digest. It is derived from the text itself, so it stays correct no matter
// which job failed or when.
type SourceDiagnosis struct {
	Title, Details string
	// Soft: a probable, not certain, problem. Hard ones mean no digest can be made.
	Soft bool
	// Kind names the sign behind a soft diagnosis (teaser or cutoff); empty for hard ones.
	Kind string
}

func diagnoseSource(s Snapshot) *SourceDiagnosis {
	err := ValidateSourceContent(s)
	var se *SourceError
	if !errors.As(err, &se) {
		if signs := incompleteSigns(s.Text); len(signs) > 0 && s.Completeness != "full" {
			where := fmt.Sprintf("Текст #%d (отримано %s, витяг %s)", s.ID, datePart(s.RetrievedAt), s.Extractor)
			if signs[0].Kind == reasonTeaser {
				return &SourceDiagnosis{Soft: true, Kind: reasonTeaser, Title: "Текст, схоже, неповний",
					Details: where + fmt.Sprintf(" закінчується рядком «%s» — це схоже на посилання на інші сторінки, а не на кінець статті. "+
						"Імовірно, збережено лише вступ або індексну сторінку; повноту оригіналу не підтверджено.", signs[0].Detail)}
			}
			return &SourceDiagnosis{Soft: true, Kind: reasonCutoff, Title: "Текст, схоже, обривається",
				Details: where + " закінчується довгим рядком без завершення речення. Імовірно, статтю обрізано (ліміт, paywall, збій витягу); " +
					"повноту оригіналу не підтверджено."}
		}
		return nil
	}
	origin := fmt.Sprintf("Текст #%d (отримано %s, витяг %s)", s.ID, datePart(s.RetrievedAt), s.Extractor)
	switch {
	case se.FooterOnly:
		line := strings.TrimSpace(strings.Split(strings.TrimSpace(s.Text), "\n")[0])
		if r := []rune(line); len(r) > 160 {
			line = string(r[:160]) + "…"
		}
		return &SourceDiagnosis{
			Title: "Збережено лише рядок про авторські права",
			Details: origin + " складається з одного рядка: «" + line + "». Це підвал сторінки, а не стаття: витяг не знайшов основного тексту " +
				"(так буває зі сторінками-змістами, меню та сторінками, що будуються скриптом). З такого тексту дайджест не має змісту.",
		}
	case se.Challenge:
		return &SourceDiagnosis{
			Title: "Замість статті збережено перевірку браузера",
			Details: origin + " — це сторінка перевірки (капча чи «Just a moment…»), а не стаття. " +
				"Дайджест із неї не має змісту; потрібно отримати текст повторно.",
		}
	}
	return nil
}

func datePart(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return "дата невідома"
}
