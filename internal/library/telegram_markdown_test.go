package library

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTelegramMarkdownConvertsDigestSyntax(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"heading becomes bold", "## Про що матеріал", "*Про що матеріал*"},
		{"bold", "Це **важливо**.", "Це *важливо*\\."},
		{"bullets", "- перший пункт\n* другий (дужки)", "• перший пункт\n• другий \\(дужки\\)"},
		{"specials are escaped", "a_b [x] 1+1=2 #tag ~ | { } ! > `", "a\\_b \\[x\\] 1\\+1\\=2 \\#tag \\~ \\| \\{ \\} \\! \\> \\`"},
		{"link", "див. [стаття](https://a.example/p?x=1&y=(2))", "див\\. [стаття](https://a.example/p?x=1&y=(2\\))"},
		{"inline code", "викличте `go test ./...` зараз", "викличте `go test ./...` зараз"},
		{"backslash", `a\b`, `a\\b`},
		{"heading with bold inside", "### **Ключ**", "*Ключ*"},
	} {
		if got := TelegramMarkdown(c.in); got != c.want {
			t.Errorf("%s: %q != %q", c.name, got, c.want)
		}
	}
}

func TestTelegramPartFitsLimitAtParagraphBoundary(t *testing.T) {
	paragraph := strings.Repeat("слово ", 100) // 600 bytes, ~600 runes
	digest := strings.Repeat(paragraph+"\n\n", 20)
	out, cut := TelegramFit(digest, 4000)
	if !cut || utf8.RuneCountInString(out) > 4000 {
		t.Fatalf("cut=%v runes=%d", cut, utf8.RuneCountInString(out))
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "слово") {
		t.Fatalf("must end at a paragraph boundary, ends with %q", out[len(out)-20:])
	}
	short, cut := TelegramFit("короткий текст", 4000)
	if cut || short != "короткий текст" {
		t.Fatalf("%q %v", short, cut)
	}
}
