package library

import (
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
		return &SourceError{Kind: "source", FooterOnly: true, Message: "збережено лише copyright, без змісту статті; отримайте текст повторно або вставте його вручну, повтор дайджесту не допоможе"}
	}
	return nil
}
