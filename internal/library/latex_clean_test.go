package library

import (
	"strings"
	"testing"
)

func TestCleanLatex(t *testing.T) {
	cases := map[string]string{
		`A $\rightarrow$ B`:                       "A → B",
		`$\alpha \leq \beta$ і \to C`:             "α ≤ β і → C",
		`\text{слово} та $x \times y$`:            "слово та x × y",
		`$\mathcal{L} \approx \infty$`:            "L ≈ ∞",
		"Ціна $5 і $10 тут":                       "Ціна $5 і $10 тут",
		"`$HOME/bin` і $\\pi$":                    "`$HOME/bin` і π",
		"```\necho $HOME $\\to$\n```\n$\\to$ вне": "```\necho $HOME $\\to$\n```\n→ вне",
		`\total і \tox`:                           `\total і \tox`,
		"без формул":                              "без формул",
	}
	for in, want := range cases {
		if got := cleanLatex(in); got != want {
			t.Errorf("cleanLatex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompleteDigestStoresCleanedLatex(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/latex")
	manual(t, s, id, "Source text")
	j := claim(t, s)
	md := fixtureDigest + "\n$\\rightarrow$ ключовий перехід\n"
	if err := s.CompleteDigest(j, Digest{Markdown: md, ActualModel: j.Model, Provider: "fixture"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.DigestOf(id)
	if err != nil || d == nil || strings.Contains(d.Markdown, `\rightarrow`) || !strings.Contains(d.Markdown, "→ ключовий перехід") {
		t.Fatalf("latex must be cleaned at generation time: %v %v", d, err)
	}
}
