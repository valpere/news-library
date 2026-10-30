package library

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

type SearchResult struct {
	Items             []Item `json:"items"`
	Mode              string `json:"mode"`
	Warning           string `json:"warning,omitempty"`
	Indexed, Eligible int
}
type semanticHit struct {
	id         int64
	start, end int
	score      float64
}

func searchMode(mode string) (string, error) {
	if mode == "" {
		mode = "fts"
	}
	if mode != "fts" && mode != "vector" && mode != "hybrid" {
		return "", errors.New("invalid search mode")
	}
	return mode, nil
}
func (s *Semantic) Search(ctx context.Context, f Filters, mode string) (SearchResult, error) {
	f.Query = strings.TrimSpace(f.Query)
	mode, err := searchMode(mode)
	out := SearchResult{Mode: mode, Items: []Item{}}
	if err != nil {
		return out, err
	}
	if len(f.Query) > 4096 || !utf8.ValidString(f.Query) {
		return out, errors.New("search query must be valid UTF-8 up to 4096 bytes")
	}
	if mode == "fts" || strings.TrimSpace(f.Query) == "" {
		out.Items, err = s.Store.Search(f)
		return out, err
	}
	// Apply every metadata filter before ranking; no fixed candidate limit can
	// hide matches with a low score or unknown rating.
	base := f
	base.Query = ""
	if base.Sort == "relevance" {
		base.Sort = "added"
	}
	candidates, err := s.Store.Search(base)
	if err != nil {
		return out, err
	}
	eligible := map[int64]Item{}
	for _, item := range candidates {
		eligible[item.ID] = item
		if item.HasText && item.FilterState != "rejected" {
			out.Eligible++
		}
	}
	fallback := func() (SearchResult, error) {
		out.Mode = "fts"
		out.Warning = "Пошук за змістом недоступний; показано результати текстового пошуку."
		out.Items, err = s.Store.Search(f)
		return out, err
	}
	if s.Embedder == nil {
		return fallback()
	}
	work, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	revision, err := s.Embedder.Revision(work)
	if err != nil {
		return fallback()
	}
	version := s.version(revision)
	join := ` FROM embedding_chunks c JOIN embedding_runs e ON e.item_id=c.item_id AND e.version=c.version JOIN active_items i ON i.id=c.item_id WHERE c.version=? AND e.state='done' AND i.filter_state IN ('passed','overridden')`
	rows, err := s.Store.DB.QueryContext(ctx, "SELECT DISTINCT i.id"+join, version)
	if err != nil {
		return out, err
	}
	indexed := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		if _, ok := eligible[id]; ok {
			indexed[id] = true
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	out.Indexed = len(indexed)
	if out.Indexed == 0 {
		return fallback()
	}
	// No network call while holding the service's single DB connection.
	vector, err := s.Embedder.Embed(work, f.Query, revision)
	if err != nil {
		return fallback()
	}
	vector, err = normalized(vector, s.Dimensions)
	if err != nil {
		return fallback()
	}
	rows, err = s.Store.DB.QueryContext(ctx, "SELECT i.id,c.start_byte,c.end_byte,c.vector"+join, version)
	if err != nil {
		return out, err
	}
	best := map[int64]semanticHit{}
	corrupt := false
	for rows.Next() {
		var hit semanticHit
		var data string
		if err = rows.Scan(&hit.id, &hit.start, &hit.end, &data); err != nil {
			break
		}
		if _, ok := eligible[hit.id]; !ok {
			continue
		}
		var v []float64
		if json.Unmarshal([]byte(data), &v) != nil {
			corrupt = true
			break
		}
		var e error
		v, e = normalized(v, s.Dimensions)
		if e != nil {
			corrupt = true
			break
		}
		score := 0.0
		for i, n := range v {
			score += n * vector[i]
		}
		if score <= 0 {
			continue
		}
		hit.score = score
		previous, ok := best[hit.id]
		if !ok || hit.score > previous.score || (hit.score == previous.score && hit.start < previous.start) {
			best[hit.id] = hit
		}
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	if corrupt {
		return fallback()
	}

	hits := make([]semanticHit, 0, len(best))
	for _, hit := range best {
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score == hits[j].score {
			return hits[i].id < hits[j].id
		}
		return hits[i].score > hits[j].score
	})
	scores := map[int64]float64{}
	for rank, hit := range hits {
		scores[hit.id] = 1 / float64(60+rank+1)
	}
	if mode == "hybrid" {
		lexical := f
		lexical.Sort = "relevance"
		words, e := s.Store.Search(lexical)
		if e != nil {
			return out, e
		}
		for rank, item := range words {
			scores[item.ID] += 1 / float64(60+rank+1)
		}
	}
	for _, item := range candidates {
		if _, ok := scores[item.ID]; ok {
			out.Items = append(out.Items, item)
		}
	}
	if f.Sort == "" || f.Sort == "relevance" {
		sort.Slice(out.Items, func(i, j int) bool {
			a, b := out.Items[i].ID, out.Items[j].ID
			if scores[a] == scores[b] {
				return a < b
			}
			return scores[a] > scores[b]
		})
	}
	if out.Indexed < out.Eligible {
		out.Warning = "Частина матеріалів ще не має індексу за змістом; вони доступні в текстовому пошуку."
	}
	if len(out.Items) > 100 {
		out.Items = out.Items[:100]
		out.Warning += " Показано перші 100 результатів; уточніть запит або фільтри."
	}
	// Snippets come from hash-verified primary text and reference its immutable
	// snapshot and exact UTF-8 byte offsets. No extracted text copied into SQLite.
	for n := range out.Items {
		item := &out.Items[n]
		hit, ok := best[item.ID]
		if !ok {
			continue
		}
		snapshot, e := s.Store.SourceText(hit.id)
		if e != nil {
			return out, e
		}
		if !byteBoundary(snapshot.Text, hit.start) || !byteBoundary(snapshot.Text, hit.end) || hit.end < hit.start || !utf8.ValidString(snapshot.Text[hit.start:hit.end]) {
			return out, errors.New("invalid chunk offsets")
		}
		excerpt := snapshot.Text[hit.start:hit.end]
		end := hit.end
		if len(excerpt) > 800 {
			excerpt = excerpt[:800]
			for !utf8.ValidString(excerpt) {
				excerpt = excerpt[:len(excerpt)-1]
			}
			end = hit.start + len(excerpt)
		}
		item.MatchSnippet = excerpt
		item.MatchStart = hit.start
		item.MatchEnd = end
	}
	return out, nil
}
