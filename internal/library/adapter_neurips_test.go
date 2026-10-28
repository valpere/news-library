package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// rscPage builds a page the way the reader does: the paper is a JSON value
// inside Next.js "self.__next_f.push" script chunks, not in the visible HTML.
func rscPage(t *testing.T, paper any) string {
	t.Helper()
	payload, err := json.Marshal(paper)
	if err != nil {
		t.Fatal(err)
	}
	line := `36:[["$","$L3b",null,{"paperID":"116441","paperJSON":` + string(payload) + `}]]` + "\n"
	half := len(line) / 2 // chunks must be re-joined in order
	chunk := func(s string) string {
		q, _ := json.Marshal(s)
		return `<script>self.__next_f.push([1,` + string(q) + `])</script>`
	}
	return `<html><head><title>Read Paper on Bytez</title></head><body><nav>bytez Search Feed</nav>` + chunk(line[:half]) + chunk(line[half:]) + `</body></html>`
}

type paperRun struct {
	Text    string `json:"text"`
	Element string `json:"element,omitempty"`
}
type paperSection struct {
	Heading    string       `json:"heading"`
	Paragraphs [][]paperRun `json:"paragraphs"`
}

func samplePaper() map[string]any {
	return map[string]any{"title": "Harnessing the Universal Geometry of Embeddings", "sections": []paperSection{
		{Heading: "Abstract", Paragraphs: [][]paperRun{{{Text: "We introduce the first method for translating text embeddings."}}}},
		{Heading: "1 Introduction", Paragraphs: [][]paperRun{
			{{Text: "Text embeddings are the backbone"}, {Text: "of modern NLP."}},
			{{Text: "Figure 1: Left: input embeddings.", Element: "figcaption"}},
		}},
		{Heading: "7 Ablations", Paragraphs: [][]paperRun{{{Text: "We ablate each loss term."}}}},
		{Heading: "Empty section"},
	}}
}

func neuripsFixture(t *testing.T, body string, status int) (*Fetcher, *url.URL) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/neurips/116441/paper" {
			if status != 0 && status != 200 {
				http.Error(w, "no", status)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Poster page</title></head><body><article><p>`+strings.Repeat("Лише абстракт постера. ", 20)+`</p></article></body></html>`)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	a := neuripsAdapter{pageHosts: map[string]bool{u.Hostname(): true}, paperBase: srv.URL}
	return &Fetcher{client: srv.Client(), MaxBytes: 1 << 20, Adapters: []SourceAdapter{a}}, u
}

func TestNeuripsAdapterMatchesOnlyPosterPages(t *testing.T) {
	a := neuripsAdapter{pageHosts: map[string]bool{"neurips.cc": true, "www.neurips.cc": true}}
	for raw, want := range map[string]bool{
		"https://neurips.cc/virtual/2025/loc/san-diego/poster/116441": true,
		"https://neurips.cc/virtual/2024/poster/93421":                true,
		"https://www.neurips.cc/virtual/2025/oral/116441":             true,
		"https://neurips.cc/virtual/2025/loc/san-diego/poster/abc":    false,
		"https://neurips.cc/Conferences/2025":                         false,
		"https://example.com/virtual/2025/poster/116441":              false,
		"https://arxiv.org/abs/2505.12540":                            false,
	} {
		u, _ := url.Parse(raw)
		if got := a.Match(u); got != want {
			t.Errorf("%s: got %v want %v", raw, got, want)
		}
	}
}

func TestNeuripsAdapterExtractsTheWholePaper(t *testing.T) {
	f, u := neuripsFixture(t, rscPage(t, samplePaper()), 0)
	snap, err := f.Fetch(context.Background(), u.String()+"/virtual/2025/loc/san-diego/poster/116441")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Harnessing the Universal Geometry of Embeddings", "## Abstract", "## 1 Introduction", "## 7 Ablations",
		"Text embeddings are the backbone of modern NLP.", "Figure 1: Left: input embeddings.", "We ablate each loss term."} {
		if !strings.Contains(snap.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, snap.Text)
		}
	}
	if strings.Contains(snap.Text, "Empty section") || strings.Contains(snap.Text, "Лише абстракт") {
		t.Errorf("empty sections and the generic poster text must not appear:\n%s", snap.Text)
	}
	if snap.Extractor != "neurips-bytez-v1" || snap.Title != "Harnessing the Universal Geometry of Embeddings" || snap.Completeness != "unknown" {
		t.Fatalf("%q %q %q", snap.Extractor, snap.Title, snap.Completeness)
	}
	if !strings.Contains(snap.Warning, "Bytez") {
		t.Fatalf("the warning must say where the text came from: %q", snap.Warning)
	}
}

func TestNeuripsAdapterFallsBackWhenTheReaderFormatChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"no paper json": {body: `<html><script>self.__next_f.push([1,"36:[]"])</script></html>`},
		"empty paper":   {body: rscPage(t, map[string]any{"title": "T", "sections": []paperSection{}})},
		"http error":    {body: "", status: 503},
	} {
		f, u := neuripsFixture(t, tc.body, tc.status)
		snap, err := f.Fetch(context.Background(), u.String()+"/virtual/2025/loc/san-diego/poster/116441")
		if err != nil || !strings.Contains(snap.Text, "Лише абстракт постера") {
			t.Errorf("%s: the poster text must still be saved: %+v %v", name, snap, err)
			continue
		}
		if !strings.Contains(snap.Warning, "neurips-bytez-v1") || snap.Extractor == "neurips-bytez-v1" {
			t.Errorf("%s: the failure must be visible and the generic extractor recorded: %q / %q", name, snap.Warning, snap.Extractor)
		}
	}
}
