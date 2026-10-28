package library

import (
	"strings"
	"unicode/utf8"
)

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
