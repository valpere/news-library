package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SearchEvent is one human search with the result IDs shown. Judgments about
// results are separate, append-only and never touch usefulness ratings.
type SearchEvent struct {
	ID                      int64
	Query, Mode, ActualMode string
	Filters                 string
	ResultIDs               []int64
	CreatedAt, NotFoundAt   string
}

const searchEventReuse = 10 * time.Minute

// RecordSearch stores a search; an identical one (query, mode, filters and
// ordered results) within searchEventReuse reuses the event so reloads do not
// duplicate it.
func (s *Store) RecordSearch(query, mode, actualMode, filters string, ids []int64) (int64, error) {
	if ids == nil {
		ids = []int64{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	since := time.Now().UTC().Add(-searchEventReuse).Format("2006-01-02T15:04:05.000000000Z")
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM search_events WHERE query=? AND mode=? AND actual_mode=? AND filters=? AND result_ids=? AND created_at>=? ORDER BY id DESC LIMIT 1`,
		query, mode, actualMode, filters, string(raw), since).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.DB.Exec(`INSERT INTO search_events(query,mode,actual_mode,filters,result_ids,created_at) VALUES(?,?,?,?,?,?)`,
		query, mode, actualMode, filters, string(raw), nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SearchEvent returns the event and the latest judgment per item.
func (s *Store) SearchEvent(id int64) (SearchEvent, map[int64]int, error) {
	var e SearchEvent
	var raw string
	var nf sql.NullString
	err := s.DB.QueryRow(`SELECT id,query,mode,actual_mode,filters,result_ids,created_at,not_found_at FROM search_events WHERE id=?`, id).
		Scan(&e.ID, &e.Query, &e.Mode, &e.ActualMode, &e.Filters, &raw, &e.CreatedAt, &nf)
	if err != nil {
		return e, nil, err
	}
	e.NotFoundAt = nf.String
	if err = json.Unmarshal([]byte(raw), &e.ResultIDs); err != nil {
		return e, nil, err
	}
	rows, err := s.DB.Query(`SELECT item_id,score FROM search_judgments WHERE id IN (SELECT max(id) FROM search_judgments WHERE event_id=? GROUP BY item_id)`, id)
	if err != nil {
		return e, nil, err
	}
	defer rows.Close()
	judgments := map[int64]int{}
	for rows.Next() {
		var item int64
		var score int
		if err = rows.Scan(&item, &score); err != nil {
			return e, nil, err
		}
		judgments[item] = score
	}
	return e, judgments, rows.Err()
}

// JudgeSearchResult records relevance of a shown result: 0 irrelevant,
// 1 partial, 2 exactly what was needed.
func (s *Store) JudgeSearchResult(eventID, itemID int64, score int) error {
	if score < 0 || score > 2 {
		return errors.New("оцінка відповідності має бути 0, 1 або 2")
	}
	e, _, err := s.SearchEvent(eventID)
	if err != nil {
		return err
	}
	if !slices.Contains(e.ResultIDs, itemID) {
		return errors.New("матеріалу не було у видачі цього пошуку")
	}
	_, err = s.DB.Exec(`INSERT INTO search_judgments(event_id,item_id,score,recorded_at) VALUES(?,?,?,?)`, eventID, itemID, score, nowUTC())
	return err
}

// MarkSearchNotFound dates the first "did not find what I needed" mark.
func (s *Store) MarkSearchNotFound(eventID int64) error {
	res, err := s.DB.Exec(`UPDATE search_events SET not_found_at=? WHERE id=? AND not_found_at IS NULL`, nowUTC(), eventID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, _, err = s.SearchEvent(eventID); err != nil {
			return err
		}
	}
	return nil
}

func safeBack(raw string) string {
	if raw == "/browse" || strings.HasPrefix(raw, "/browse?") {
		return raw
	}
	return "/browse"
}

func (w *Web) searchJudgment(rw http.ResponseWriter, r *http.Request) {
	event, e1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	item, e2 := strconv.ParseInt(r.PostForm.Get("item"), 10, 64)
	score, e3 := strconv.Atoi(r.PostForm.Get("score"))
	if e1 != nil || e2 != nil || e3 != nil {
		http.Error(rw, "некоректна оцінка пошуку", http.StatusBadRequest)
		return
	}
	if err := w.Store.JudgeSearchResult(event, item, score); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, safeBack(r.PostForm.Get("back")), http.StatusSeeOther)
}

func (w *Web) searchNotFound(rw http.ResponseWriter, r *http.Request) {
	event, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(rw, "некоректний пошук", http.StatusBadRequest)
		return
	}
	if err = w.Store.MarkSearchNotFound(event); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(rw, r, safeBack(r.PostForm.Get("back")), http.StatusSeeOther)
}

type searchScore struct {
	Score int
	Label string
}

func searchScores() []searchScore {
	return []searchScore{{0, "не те"}, {1, "частково"}, {2, "точно те"}}
}

func searchJudgmentLabel(j map[int64]int, id int64) string {
	if score, ok := j[id]; ok {
		return searchScores()[score].Label
	}
	return ""
}
