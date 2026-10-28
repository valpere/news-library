package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// NeurIPS poster pages show only the abstract in their own HTML; the paper
// itself is an iframe from the Bytez reader, which is built by scripts. The
// reader's server-rendered page nevertheless carries the whole paper as JSON
// inside Next.js "self.__next_f.push" chunks, so plain HTTP is enough.
//
// This depends on the internal format of a third-party site. If it changes the
// adapter fails (no paper found) and the generic extraction is used with a
// warning; bump the name's version when the parser is adapted.
const neuripsAdapterName = "neurips-bytez-v1"

var neuripsPosterPath = regexp.MustCompile(`^/virtual/\d{4}/(?:loc/[^/]+/)?(?:poster|oral|spotlight)/(\d+)/?$`)

type neuripsAdapter struct {
	pageHosts map[string]bool // hosts of poster pages
	paperBase string          // origin of the reader, e.g. https://bytez.com
}

func newNeuripsAdapter() neuripsAdapter {
	return neuripsAdapter{pageHosts: map[string]bool{"neurips.cc": true, "www.neurips.cc": true}, paperBase: "https://bytez.com"}
}

func (a neuripsAdapter) Name() string { return neuripsAdapterName }

func (a neuripsAdapter) Hosts() []string {
	hosts := make([]string, 0, len(a.pageHosts))
	for h := range a.pageHosts {
		hosts = append(hosts, h)
	}
	return hosts
}

func (a neuripsAdapter) Match(u *url.URL) bool {
	return a.pageHosts[strings.ToLower(u.Hostname())] && neuripsPosterPath.MatchString(u.Path)
}

type neuripsPaper struct {
	Title    string `json:"title"`
	Sections []struct {
		Heading    string `json:"heading"`
		Paragraphs [][]struct {
			Text string `json:"text"`
		} `json:"paragraphs"`
	} `json:"sections"`
}

var nextChunk = regexp.MustCompile(`self\.__next_f\.push\(\[1,("(?:[^"\\]|\\.)*")\]\)`)

func (a neuripsAdapter) Extract(ctx context.Context, f *Fetcher, u *url.URL) (Snapshot, error) {
	m := neuripsPosterPath.FindStringSubmatch(u.Path)
	paperURL := a.paperBase + "/docs/neurips/" + m[1] + "/paper"
	data, _, _, partial, err := f.get(ctx, paperURL)
	if err != nil {
		return Snapshot{}, err
	}
	if partial {
		return Snapshot{}, errors.New("відповідь читалки обрізана")
	}
	paper, err := parseNeuripsPaper(data)
	if err != nil {
		return Snapshot{}, err
	}
	var b strings.Builder
	words := 0
	if paper.Title != "" {
		b.WriteString(paper.Title + "\n\n")
	}
	for _, s := range paper.Sections {
		var paras []string
		for _, runs := range s.Paragraphs {
			var parts []string
			for _, r := range runs {
				if t := strings.TrimSpace(r.Text); t != "" {
					parts = append(parts, t)
				}
			}
			if len(parts) > 0 {
				paras = append(paras, strings.Join(parts, " "))
			}
		}
		if len(paras) == 0 {
			continue
		}
		b.WriteString("## " + strings.TrimSpace(s.Heading) + "\n\n" + strings.Join(paras, "\n\n") + "\n\n")
		words += len(strings.Fields(strings.Join(paras, " ")))
	}
	if words == 0 {
		return Snapshot{}, errors.New("у відповіді читалки немає тексту статті")
	}
	return Snapshot{
		Text: strings.TrimSpace(b.String()), Title: paper.Title, Completeness: "unknown",
		Warning: fmt.Sprintf("Повний текст статті з читалки Bytez (%d слів)", words),
	}, nil
}

// parseNeuripsPaper joins the script chunks in order and decodes the object that
// follows "paperJSON".
func parseNeuripsPaper(page []byte) (neuripsPaper, error) {
	var joined strings.Builder
	for _, m := range nextChunk.FindAllSubmatch(page, -1) {
		var s string
		if err := json.Unmarshal(m[1], &s); err != nil {
			continue
		}
		joined.WriteString(s)
	}
	text := joined.String()
	const key = `"paperJSON":`
	i := strings.Index(text, key)
	if i < 0 {
		return neuripsPaper{}, errors.New("у сторінці читалки немає paperJSON (формат змінився?)")
	}
	var paper neuripsPaper
	dec := json.NewDecoder(bytes.NewReader([]byte(text[i+len(key):])))
	if err := dec.Decode(&paper); err != nil {
		return neuripsPaper{}, fmt.Errorf("paperJSON не розібрано: %w", err)
	}
	return paper, nil
}
