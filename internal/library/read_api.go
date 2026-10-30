package library

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func requestFilters(r *http.Request) Filters {
	q := r.URL.Query()
	f := Filters{Usefulness: q.Get("usefulness"), Category: q.Get("category"), Query: q.Get("q"), Read: q.Get("read"), State: q.Get("state"), Sort: q.Get("sort"), Direction: q.Get("direction"), Filter: q.Get("filter"), ConfidenceMin: q.Get("confidence_min"), ConfidenceMax: q.Get("confidence_max")}
	for _, tag := range strings.Split(q.Get("tags"), ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			f.Tags = append(f.Tags, tag)
		}
	}
	return f
}
func (w *Web) readAPI(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-store")
	// Authenticate before routing; token grants no access to mutation methods.
	if w.ReadToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+w.ReadToken)) != 1 {
		http.Error(rw, "read credentials required", http.StatusUnauthorized)
		return
	}
	if r.Method != "GET" {
		http.Error(rw, "read API only supports GET", http.StatusMethodNotAllowed)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		http.Error(rw, "read API is for authenticated tools", http.StatusForbidden)
		return
	}
	jsonResponse := func(v any) {
		rw.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(rw).Encode(v)
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/read/")
	if path == "search" {
		f := requestFilters(r)
		result, err := w.search(r, f, r.URL.Query().Get("mode"))
		if err != nil {
			http.Error(rw, "invalid or unavailable search", 400)
			return
		}
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var e error
			limit, e = strconv.Atoi(raw)
			if e != nil || limit < 1 || limit > 100 {
				http.Error(rw, "limit must be 1..100", 400)
				return
			}
		}
		truncated := len(result.Items) > limit
		if truncated {
			result.Items = result.Items[:limit]
		}
		items := []map[string]any{}
		for _, item := range result.Items {
			items = append(items, readItem(item))
		}
		jsonResponse(map[string]any{"items": items, "mode": result.Mode, "warning": result.Warning, "indexed": result.Indexed, "eligible": result.Eligible, "truncated": truncated})
		return
	}
	if path == "connections" {
		notes, err := w.Store.Connections(r.URL.Query().Get("q"), 0, 0)
		if err != nil {
			http.Error(rw, "notes unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonResponse(map[string]any{"notes": notes, "limit": 100})
		return
	}
	if strings.HasPrefix(path, "connections/") {
		id, err := strconv.ParseInt(strings.TrimPrefix(path, "connections/"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(rw, r)
			return
		}
		version, err := connectionVersion(r)
		if err != nil {
			http.Error(rw, "invalid version", 400)
			return
		}
		note, err := w.Store.Connection(id, version)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		jsonResponse(note)
		return
	}
	pieces := strings.Split(path, "/")
	if len(pieces) != 2 && len(pieces) != 3 {
		http.NotFound(rw, r)
		return
	}
	if pieces[0] != "items" {
		http.NotFound(rw, r)
		return
	}
	id, err := strconv.ParseInt(pieces[1], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(rw, r)
		return
	}
	item, err := w.Store.Item(id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(rw, r)
		return
	}
	if err != nil {
		http.Error(rw, "library unavailable", http.StatusServiceUnavailable)
		return
	}
	kind := "item"
	if len(pieces) == 3 {
		kind = pieces[2]
	}
	if kind != "item" && kind != "context" && kind != "fragment" {
		http.NotFound(rw, r)
		return
	}
	payload := readItem(item)
	if item.Selected.Valid {
		snapshot, err := w.Store.Snapshot(item.Selected.Int64)
		if err != nil {
			http.Error(rw, "source unavailable", http.StatusServiceUnavailable)
			return
		}
		payload["confidence"] = nil
		payload["text_kind"] = "source"
		var digest *Digest
		if kind != "fragment" {
			digest, err = w.Store.DigestForSnapshot(snapshot.ID)
			if err != nil {
				http.Error(rw, "digest unavailable", http.StatusServiceUnavailable)
				return
			}
			if digest != nil {
				meta := map[string]any{"model": digest.ActualModel, "provider": digest.Provider, "prompt_version": digest.PromptVersion, "created_at": digest.CreatedAt}
				if digest.Prediction != nil {
					payload["confidence"] = digest.Prediction.Probability
					meta["prediction"] = map[string]any{"probability": digest.Prediction.Probability, "reason": digest.Prediction.Reason}
				}
				if kind == "item" {
					meta["markdown"] = digest.Markdown
				}
				payload["digest"] = meta
			}
		}
		text := snapshot.Text
		originalLength := len(text)
		start, end := 0, len(text)
		limit := 4000
		if kind == "fragment" {
			for _, entry := range []struct {
				name   string
				target *int
			}{{"start", &start}, {"end", &end}} {
				if raw := r.URL.Query().Get(entry.name); raw != "" {
					value, e := strconv.Atoi(raw)
					if e != nil {
						http.Error(rw, "invalid offsets", 400)
						return
					}
					*entry.target = value
				}
			}
			if !byteBoundary(text, start) || !byteBoundary(text, end) || end < start || end-start > 16384 || !utf8.ValidString(text[start:end]) {
				http.Error(rw, "fragment needs UTF-8 byte boundaries and at most 16384 bytes", 400)
				return
			}
			text = text[start:end]
		} else if kind == "context" {
			if digest != nil {
				text = digest.Markdown
				payload["context_kind"] = "digest"
				payload["text_kind"] = "digest"
			} else {
				payload["context_kind"] = "source_excerpt"
			}
			end = len(text)
			originalLength = len(text)
			if len(text) > limit {
				text = text[:limit]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
				end = len(text)
			}
		}
		payload["source"] = map[string]any{"id": snapshot.ID, "url": item.URL, "hash": snapshot.Hash, "retrieved_at": snapshot.RetrievedAt, "origin": snapshot.Origin, "extractor": snapshot.Extractor, "completeness": snapshot.Completeness, "incomplete_signs": Assess(snapshot).Reasons, "warning": snapshot.Warning}
		payload["text"] = text
		payload["start_byte"] = start
		payload["end_byte"] = end
		payload["truncated"] = kind == "context" && len(text) < originalLength
	} else {
		payload["text_available"] = false
	}
	backlinks, err := w.Store.Connections("", id, 0)
	if err != nil {
		http.Error(rw, "backlinks unavailable", http.StatusServiceUnavailable)
		return
	}
	if item.Selected.Valid {
		knowledge, e := w.Store.Knowledge(id, item.Selected.Int64)
		if e != nil {
			http.Error(rw, "knowledge unavailable", http.StatusServiceUnavailable)
			return
		}
		if knowledge == nil {
			payload["knowledge"] = nil
		} else {
			payload["knowledge"] = map[string]any{"id": knowledge.ID, "run_id": knowledge.RunID, "item_id": knowledge.ItemID, "category": knowledge.Category, "tags": knowledge.Tags, "questions": knowledge.Questions, "author": "model", "model": knowledge.Model, "prompt_version": knowledge.PromptVersion, "created_at": knowledge.CreatedAt, "file_error": knowledge.FileError}
		}
	}
	payload["connections"] = backlinks
	jsonResponse(payload)
}
func readItem(item Item) map[string]any {
	var score, ratedAt, confidence any
	if item.Score.Valid {
		score = item.Score.Int64
	}
	if item.RatedAt.Valid {
		ratedAt = item.RatedAt.String
	}
	if item.Confidence.Valid {
		confidence = item.Confidence.Float64
	}
	return map[string]any{"id": item.ID, "url": item.URL, "title": item.Title, "read": item.Read, "rating": score, "rated_at": ratedAt, "rating_label": ratingLabel(item.Score), "confidence": confidence, "filter_state": item.FilterState, "state": item.State, "tags": item.Tags, "snippet": item.MatchSnippet, "start_byte": item.MatchStart, "end_byte": item.MatchEnd}
}
func (w *Web) search(r *http.Request, f Filters, mode string) (SearchResult, error) {
	semantic := w.Semantic
	if semantic == nil {
		semantic = &Semantic{Store: w.Store}
	}
	return semantic.Search(r.Context(), f, mode)
}
