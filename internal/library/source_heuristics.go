package library

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const minArticleWords = 150

var truncationMarkers = []string{
	"continue reading", "read more", "read the full", "subscribe to continue", "subscribe to read",
	"sign in to read", "log in to read", "explore more", "more articles", "see all articles", "view all articles", "читати далі", "читайте далі", "продовжити читання", "підпишіться, щоб читати",
}

// trailingMarker returns the last line when it is a short "read more" style
// pointer (an index or teaser page), or "".
func trailingMarker(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) < 2 || utf8.RuneCountInString(last) >= 80 {
		return ""
	}
	low := strings.ToLower(last)
	for _, m := range truncationMarkers {
		if strings.Contains(low, m) {
			return last
		}
	}
	return ""
}

// partialSourceSuspicion returns a reason when extracted HTML text looks cut
// off or truncated, or "" otherwise. It is a hint, never proof of completeness.
func partialSourceSuspicion(text string) string {
	text = strings.TrimSpace(text)
	if n := len(strings.Fields(text)); n < minArticleWords {
		return fmt.Sprintf("лише %d слів", n)
	}
	if marker := trailingMarker(text); marker != "" {
		return "наприкінці маркер обрізання «" + marker + "»"
	}
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	// A long final paragraph that stops mid-sentence; short last lines are
	// usually headings, list items or code.
	if utf8.RuneCountInString(last) >= 40 {
		r, _ := utf8.DecodeLastRuneInString(last)
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ',' {
			return "останній абзац обривається без завершення речення"
		}
	}
	return ""
}
