package library

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

const BatchURLLimit = 500
const BatchInputLimit = 64 << 10

type BatchURLResult struct {
	Line          int
	URL           string
	ItemID        int64
	Status, Error string
}

// Each nonempty line uses the same durable, deduplicated transaction as AddURL.
// Validate the whole input's bounds before any writes, then isolate row errors.
func (s *Store) AddURLs(raw string) ([]BatchURLResult, error) {
	if len(raw) > BatchInputLimit || !utf8.ValidString(raw) {
		return nil, errors.New("список має бути UTF-8 і не перевищувати 64 КіБ")
	}
	lines := strings.Split(raw, "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	if count == 0 || count > BatchURLLimit {
		return nil, errors.New("вставте від 1 до 500 посилань, по одному на рядок")
	}
	results := make([]BatchURLResult, 0, count)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result := BatchURLResult{Line: i + 1, URL: line, Status: "failed"}
		if _, err := CanonicalURL(line); err != nil {
			result.Error = err.Error()
		} else {
			id, created, err := s.addURL(line)
			if err != nil {
				result.Error = "Не вдалося зберегти; спробуйте цей рядок ще раз."
			} else {
				result.ItemID = id
				result.Status = "existing"
				if reason, e := s.TrashReason(id); e != nil {
					return results, e
				} else if reason != "" {
					result.Status = "trashed"
					result.Error = "URL уже в помийці: " + reason
				}
				if created {
					result.Status = "added"
				}
			}
		}
		results = append(results, result)
	}
	return results, nil
}
func (w *Web) addBatch(rw http.ResponseWriter, r *http.Request) {
	raw := r.PostForm.Get("urls")
	results, err := w.Store.AddURLs(raw)
	data := pageData{BatchResults: results}
	status := http.StatusOK
	if err != nil {
		data.BatchError = err.Error()
		data.BatchInput = raw
		status = http.StatusBadRequest
	} else {
		failed := []string{}
		for _, result := range results {
			switch result.Status {
			case "added":
				data.BatchAdded++
			case "existing", "trashed":
				data.BatchExisting++
			default:
				data.BatchFailed++
				failed = append(failed, result.URL)
			}
		}
		data.BatchInput = strings.Join(failed, "\n")
	}
	w.renderStatus(rw, "batch", data, status)
}
