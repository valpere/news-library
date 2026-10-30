package library

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const DigestRetryBatchLimit = 500

type DigestRetryCandidate struct {
	ItemID, JobID                                                int64
	Title, URL, Kind, Error, ConfigVersion, Reason, AttemptToken string
	Selectable, Checked                                          bool
}
type DigestRetryResult struct {
	ItemID int64
	Queued bool
	Reason string
}

const failedDigestSelection = `SELECT i.id,j.id,i.title,i.original_url,j.error,j.config_version,j.token,j.failure_kind FROM active_items i JOIN jobs j ON j.item_id=i.id AND j.stage='digest' WHERE j.id=(SELECT max(id) FROM jobs WHERE item_id=i.id AND stage='digest') AND j.state='failed' AND EXISTS(SELECT 1 FROM texts WHERE texts.item_id=i.id) AND NOT EXISTS(SELECT 1 FROM digests WHERE digests.item_id=i.id) AND NOT EXISTS(SELECT 1 FROM jobs WHERE jobs.item_id=i.id AND stage='digest' AND state IN ('queued','running','retry_wait'))`

func scanRetryCandidate(row interface{ Scan(...any) error }) (DigestRetryCandidate, error) {
	var c DigestRetryCandidate
	err := row.Scan(&c.ItemID, &c.JobID, &c.Title, &c.URL, &c.Error, &c.ConfigVersion, &c.AttemptToken, &c.Kind)
	if c.Kind == "" {
		c.Kind = legacyFailureKind(c.Error)
	}
	return c, err
}
func (s *Store) retryCandidatePolicy(c *DigestRetryCandidate, filtered string) {
	if s.DigestConfig == nil {
		c.Reason = "Генерацію вимкнено"
		return
	}
	if filtered == "rejected" {
		c.Reason = "Матеріал відсіяно; спочатку явно поверніть його до обробки"
		return
	}
	c.Selectable = true
	switch c.Kind {
	case "format", "temporary":
		c.Checked = true
	case "unknown":
		c.Reason = "Причина невідома; виберіть явно лише після перевірки"
	default:
		if c.ConfigVersion == s.DigestConfig.Version() {
			c.Selectable = false
			c.Reason = "Спочатку виправте налаштування, ліміти або джерело; повторіть із картки"
		} else {
			c.Reason = "Конфігурація змінилася; виберіть явно після перевірки причини"
		}
	}
}
func (s *Store) FailedDigestCandidates(after int64) ([]DigestRetryCandidate, bool, error) {
	rows, err := s.DB.Query(failedDigestSelection+` AND i.id>? ORDER BY i.id LIMIT 501`, after)
	if err != nil {
		return nil, false, err
	}
	out := []DigestRetryCandidate{}
	for rows.Next() {
		c, e := scanRetryCandidate(rows)
		if e != nil {
			rows.Close()
			return nil, false, e
		}
		out = append(out, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	truncated := len(out) > DigestRetryBatchLimit
	if truncated {
		out = out[:DigestRetryBatchLimit]
	}
	for i := range out {
		var filter string
		if err = s.DB.QueryRow("SELECT filter_state FROM items WHERE id=?", out[i].ItemID).Scan(&filter); err != nil {
			return nil, false, err
		}
		s.retryCandidatePolicy(&out[i], filter)
	}
	return out, truncated, nil
}
func (s *Store) retryFailedDigest(selected DigestRetryCandidate) error {
	if s.DigestConfig == nil {
		return errors.New("генерацію вимкнено")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanRetryCandidate(tx.QueryRow(failedDigestSelection+` AND i.id=?`, selected.ItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("стан змінився: вже обробляється, готовий або більше не має невдалої генерації")
	}
	if err != nil {
		return err
	}
	if current.JobID != selected.JobID || current.AttemptToken != selected.AttemptToken {
		return errors.New("спроба змінилася; оновіть список")
	}
	var filter, path, hash string
	if err = tx.QueryRow(`SELECT i.filter_state,v.text_path,v.hash FROM active_items i JOIN texts v ON v.item_id=i.id WHERE i.id=?`, selected.ItemID).Scan(&filter, &path, &hash); err != nil {
		return err
	}
	s.retryCandidatePolicy(&current, filter)
	if !current.Selectable {
		return errors.New(current.Reason)
	}
	if _, err = s.Files.Read(path, hash); err != nil {
		return err
	}
	queued, err := s.retryDigestTx(tx, selected.ItemID)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if !queued {
		return errors.New("джерело відсіяно правилами; генерацію не запущено")
	}
	return nil
}
func (s *Store) RetryFailedDigests(selected []DigestRetryCandidate) ([]DigestRetryResult, error) {
	if len(selected) == 0 || len(selected) > DigestRetryBatchLimit {
		return nil, errors.New("виберіть від 1 до 500 матеріалів")
	}
	seen := map[int64]bool{}
	for _, c := range selected {
		if c.ItemID <= 0 || c.JobID <= 0 || seen[c.ItemID] {
			return nil, errors.New("некоректний або повторний вибір")
		}
		seen[c.ItemID] = true
	}
	out := []DigestRetryResult{}
	for _, c := range selected {
		r := DigestRetryResult{ItemID: c.ItemID}
		if err := s.retryFailedDigest(c); err != nil {
			r.Reason = err.Error()
		} else {
			r.Queued = true
		}
		out = append(out, r)
	}
	return out, nil
}
func (w *Web) digestRetryBatchPage(rw http.ResponseWriter, r *http.Request) {
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		var e error
		after, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || after < 0 {
			http.Error(rw, "некоректна сторінка", 400)
			return
		}
	}
	candidates, truncated, err := w.Store.FailedDigestCandidates(after)
	if err != nil {
		http.Error(rw, "список невдалих дайджестів недоступний", http.StatusServiceUnavailable)
		return
	}
	next := int64(0)
	if truncated {
		next = candidates[len(candidates)-1].ItemID
	}
	includeUnknown := r.URL.Query().Get("include_unknown") == "1"
	if includeUnknown {
		for i := range candidates {
			if candidates[i].Kind == "unknown" && candidates[i].Selectable {
				candidates[i].Checked = true
			}
		}
	}
	w.render(rw, "digestRetryBatch", pageData{DigestRetryCandidates: candidates, DigestRetryTruncated: truncated, DigestRetryNext: next, DigestRetryAfter: after, DigestRetryIncludeUnknown: includeUnknown})
}
func (w *Web) digestRetryBatchSubmit(rw http.ResponseWriter, r *http.Request) {
	selected := []DigestRetryCandidate{}
	for _, value := range r.PostForm["selection"] {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			http.Error(rw, "некоректний вибір", 400)
			return
		}
		var c DigestRetryCandidate
		c.AttemptToken = parts[2]
		for i, target := range []*int64{&c.ItemID, &c.JobID} {
			v, err := strconv.ParseInt(parts[i], 10, 64)
			if err != nil {
				http.Error(rw, "некоректний вибір", 400)
				return
			}
			*target = v
		}
		selected = append(selected, c)
	}
	results, err := w.Store.RetryFailedDigests(selected)
	if err != nil {
		http.Error(rw, err.Error(), 400)
		return
	}
	queued := 0
	for _, r := range results {
		if r.Queued {
			queued++
		}
	}
	w.render(rw, "digestRetryBatchResult", pageData{DigestRetryResults: results, DigestRetryQueued: queued, DigestRetrySkipped: len(results) - queued})
}
func retrySelection(c DigestRetryCandidate) string {
	return fmt.Sprintf("%d:%d:%s", c.ItemID, c.JobID, c.AttemptToken)
}
