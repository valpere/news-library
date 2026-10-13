package library

import "strings"

// Recognize the served interstitial, not articles discussing browser challenges.
func ValidateSourceContent(s Snapshot) error {
	title := strings.ToLower(strings.TrimSpace(s.Title))
	text := strings.ToLower(strings.ReplaceAll(s.Text, "’", "'"))
	blocked := (title == "client challenge" && strings.Contains(text, "a required part of this site couldn't load")) ||
		(title == "just a moment..." && (strings.Contains(text, "verify you are human") || strings.Contains(text, "enable javascript and cookies")))
	if blocked {
		return &SourceError{Kind: "source", Challenge: true, Message: "замість статті джерело повернуло перевірку браузера; потрібне повторне отримання тексту, а не повтор дайджесту"}
	}
	return nil
}
