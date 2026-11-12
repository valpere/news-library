package library

import (
	"regexp"
	"strings"
)

// latexSymbols maps the LaTeX commands models leak into digests to plain Unicode.
var latexSymbols = func() []struct {
	re  *regexp.Regexp
	out string
} {
	pairs := [][2]string{
		{"rightarrow", "→"}, {"to", "→"}, {"leftarrow", "←"}, {"gets", "←"}, {"leftrightarrow", "↔"},
		{"Rightarrow", "⇒"}, {"Leftarrow", "⇐"}, {"uparrow", "↑"}, {"downarrow", "↓"},
		{"approx", "≈"}, {"sim", "~"}, {"neq", "≠"}, {"leq", "≤"}, {"le", "≤"}, {"geq", "≥"}, {"ge", "≥"},
		{"ll", "≪"}, {"gg", "≫"}, {"lt", "<"}, {"gt", ">"}, {"times", "×"}, {"div", "÷"}, {"pm", "±"},
		{"log", "log"}, {"ln", "ln"}, {"exp", "exp"}, {"sin", "sin"}, {"cos", "cos"}, {"tan", "tan"},
		{"lim", "lim"}, {"max", "max"}, {"min", "min"}, {"infty", "∞"}, {"pi", "π"},
		{"alpha", "α"}, {"beta", "β"}, {"gamma", "γ"}, {"delta", "δ"}, {"Delta", "Δ"}, {"epsilon", "ε"},
		{"varepsilon", "ε"}, {"sigma", "σ"}, {"theta", "θ"}, {"lambda", "λ"}, {"mu", "μ"}, {"omega", "ω"}, {"Omega", "Ω"},
	}
	var out []struct {
		re  *regexp.Regexp
		out string
	}
	for _, p := range pairs {
		out = append(out, struct {
			re  *regexp.Regexp
			out string
		}{regexp.MustCompile(`\\` + p[0] + `\b`), p[1]})
	}
	return out
}()

var (
	latexWrapped = regexp.MustCompile(`\\(?:text|mathcal|mathrm|mathbf)\{([^}]*)\}`)
	// $...$ with non-space edges, so prices like "$5 and $10" are left alone.
	latexDollar = regexp.MustCompile(`\$([^$\s\n](?:[^$\n]*[^$\s\n])?)\$`)
)

// cleanLatex turns inline LaTeX in a digest into plain text. Code fences and
// inline code are left untouched.
func cleanLatex(md string) string {
	lines := strings.Split(md, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(line, "`")
		for k := 0; k < len(parts); k += 2 { // even parts are outside inline code
			parts[k] = cleanLatexText(parts[k])
		}
		lines[i] = strings.Join(parts, "`")
	}
	return strings.Join(lines, "\n")
}

func cleanLatexText(s string) string {
	if !strings.ContainsAny(s, `\$`) {
		return s
	}
	s = latexWrapped.ReplaceAllString(s, "$1")
	for _, sym := range latexSymbols {
		s = sym.re.ReplaceAllLiteralString(s, sym.out)
	}
	return latexDollar.ReplaceAllString(s, "$1")
}
