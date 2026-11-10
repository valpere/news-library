package library

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	tgHeading = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	tgBullet  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	tgLink    = regexp.MustCompile(`^\[([^\]\n]+)\]\(((?:[^()\s]|\([^()\s]*\))+)\)`)
)

const tgSpecial = "_*[]()~`>#+-=|{}.!\\"

func tgEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < utf8.RuneSelf && strings.ContainsRune(tgSpecial, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TelegramMarkdown converts the Markdown of a digest into Telegram MarkdownV2:
// headings become bold lines, list markers bullets, **bold**, `code` and
// [links](url) are kept, everything else is escaped.
func TelegramMarkdown(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		if m := tgHeading.FindStringSubmatch(line); m != nil {
			out[i] = "*" + tgEscape(strings.ReplaceAll(m[1], "**", "")) + "*"
		} else if m := tgBullet.FindStringSubmatch(line); m != nil {
			out[i] = "• " + tgInline(m[1])
		} else {
			out[i] = tgInline(line)
		}
	}
	return strings.Join(out, "\n")
}

func tgInline(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "**"):
			if j := strings.Index(rest[2:], "**"); j > 0 {
				b.WriteString("*" + tgEscape(rest[2:2+j]) + "*")
				i += j + 4
				continue
			}
		case rest[0] == '`':
			if j := strings.IndexByte(rest[1:], '`'); j > 0 {
				b.WriteString("`" + strings.NewReplacer("\\", "\\\\").Replace(rest[1:1+j]) + "`")
				i += j + 2
				continue
			}
		case rest[0] == '[':
			if m := tgLink.FindStringSubmatch(rest); m != nil {
				url := strings.NewReplacer("\\", "\\\\", ")", "\\)").Replace(m[2])
				b.WriteString("[" + tgEscape(m[1]) + "](" + url + ")")
				i += len(m[0])
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(rest)
		b.WriteString(tgEscape(string(r)))
		i += size
	}
	return b.String()
}

// TelegramFit returns the longest prefix of md, cut at a paragraph boundary, whose
// MarkdownV2 form has at most limit characters; cut reports that text was left out.
// A single paragraph longer than the limit is cut by characters.
func TelegramFit(md string, limit int) (fit string, cut bool) {
	if utf8.RuneCountInString(TelegramMarkdown(md)) <= limit {
		return md, false
	}
	var b strings.Builder
	for _, paragraph := range strings.Split(md, "\n\n") {
		next := paragraph
		if b.Len() > 0 {
			next = b.String() + "\n\n" + paragraph
		}
		if utf8.RuneCountInString(TelegramMarkdown(next)) > limit {
			if b.Len() == 0 {
				runes := []rune(paragraph)
				for len(runes) > 0 && utf8.RuneCountInString(TelegramMarkdown(string(runes))) > limit {
					runes = runes[:len(runes)*9/10]
				}
				return string(runes), true
			}
			return b.String(), true
		}
		b.Reset()
		b.WriteString(next)
	}
	return b.String(), true
}
