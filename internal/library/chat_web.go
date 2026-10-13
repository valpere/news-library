package library

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

const chatCookie = "news_library_chat"

func chatSessionID(r *http.Request) string {
	c, e := r.Cookie(chatCookie)
	if e == nil && validChatToken(c.Value) {
		return c.Value
	}
	return ""
}
func (w *Web) chatPageData(r *http.Request) (pageData, error) {
	session, err := w.Store.ChatSession(chatSessionID(r))
	if err != nil {
		return pageData{}, err
	}
	data := pageData{Chat: session, ChatToken: rand.Text()}
	// Facets describe the whole library, rather than inventing counts for a model's search.
	current := `k.id=(SELECT a.id FROM article_knowledge a JOIN knowledge_runs kr ON kr.id=a.run_id WHERE a.item_id=i.id AND a.snapshot_id=i.selected_snapshot ORDER BY kr.updated_at DESC,a.id DESC LIMIT 1)`
	queries := []struct {
		sql      string
		target   *[]ChatFacet
		category bool
	}{
		{`SELECT k.category,count(*) FROM article_knowledge k JOIN active_items i ON i.id=k.item_id WHERE ` + current + ` AND k.category<>'' GROUP BY k.category ORDER BY count(*) DESC,k.category LIMIT 200`, &data.Categories, true},
		{`SELECT tag,count(DISTINCT item_id) FROM (SELECT item_id,tag FROM item_tags WHERE item_id IN (SELECT id FROM active_items) UNION SELECT k.item_id,j.value AS tag FROM article_knowledge k JOIN active_items i ON i.id=k.item_id,json_each(k.tags_json) j WHERE ` + current + `) GROUP BY tag ORDER BY count(DISTINCT item_id) DESC,tag LIMIT 200`, &data.ChatTags, false},
	}
	for _, q := range queries {
		rows, e := w.Store.DB.Query(q.sql)
		if e != nil {
			return data, e
		}
		for rows.Next() {
			var f ChatFacet
			if e = rows.Scan(&f.Name, &f.Count); e != nil {
				rows.Close()
				return data, e
			}
			if q.category {
				f.Selected = strings.EqualFold(f.Name, session.State.Filters.Category)
			} else {
				for _, tag := range session.State.Filters.Tags {
					if strings.EqualFold(tag, f.Name) {
						f.Selected = true
					}
				}
			}
			*q.target = append(*q.target, f)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return data, e
		}
	}
	return data, nil
}
func (w *Web) chatHome(rw http.ResponseWriter, r *http.Request) {
	// Existing bookmarked search URLs still show explicitly requested results.
	if r.URL.RawQuery != "" {
		w.list(rw, r)
		return
	}
	if chatSessionID(r) == "" {
		id := rand.Text()
		http.SetCookie(rw, &http.Cookie{Name: chatCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 365 * 24 * 3600})
	}
	data, err := w.chatPageData(r)
	if err != nil {
		http.Error(rw, "не вдалося прочитати діалог", 500)
		return
	}
	w.render(rw, "chat", data)
}
func (w *Web) chatSubmit(rw http.ResponseWriter, r *http.Request) {
	revision, e := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	input := ChatInput{Text: strings.TrimSpace(r.PostForm.Get("message")), Event: r.PostForm.Get("event"), Value: r.PostForm.Get("value"), Token: r.PostForm.Get("token"), Revision: revision}
	if e != nil || revision < 0 || len(input.Text) > 65536 || len(input.Value) > 1024 || !utf8.ValidString(input.Text) || !utf8.ValidString(input.Value) || len(input.Event) > 32 {
		http.Error(rw, "повідомлення завелике або некоректне", 400)
		return
	}
	if input.Event != "" {
		label := chatEventLabel(input.Event, input.Value)
		if label == "" {
			http.Error(rw, "невідома кнопка чату", 400)
			return
		}
		input.Text = label
	}
	if input.Text == "" {
		http.Error(rw, "напишіть повідомлення або вставте URL", 400)
		return
	}
	// Catch oversized URL batches before calling a model or admitting any URL.
	if len(extractChatURLs(input.Text)) > 500 {
		http.Error(rw, "пачка може містити до 500 посилань", 400)
		return
	}
	session, _, err := w.handleChatTurn(r, chatSessionID(r), input)
	if session.ID != "" {
		http.SetCookie(rw, &http.Cookie{Name: chatCookie, Value: session.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 365 * 24 * 3600})
	}
	if err != nil {
		status := 400
		if errors.Is(err, ErrChatBusy) || errors.Is(err, ErrChatStale) {
			status = 409
		}
		http.Error(rw, err.Error(), status)
		return
	}
	if r.Header.Get("X-Chat-Request") == "1" {
		data, err := w.chatPageData(&http.Request{Header: http.Header{"Cookie": {chatCookie + "=" + session.ID}}})
		if err != nil {
			http.Error(rw, "дію збережено; оновіть сторінку", 500)
			return
		}
		data.CSRF = w.CSRF
		var out strings.Builder
		if err = w.tmpl.ExecuteTemplate(&out, "chat-workspace", data); err != nil {
			http.Error(rw, "дію збережено; оновіть сторінку", 500)
			return
		}
		rw.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(rw).Encode(map[string]string{"html": out.String()})
		return
	}
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}
func (w *Web) chatNew(rw http.ResponseWriter, r *http.Request) {
	http.SetCookie(rw, &http.Cookie{Name: chatCookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

func (s *Store) chatVocabulary() (map[string][]string, error) {
	v, e := s.knowledgeVocabulary()
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.Query("SELECT DISTINCT tag FROM item_tags ORDER BY tag LIMIT 200")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var tag string
		if e = rows.Scan(&tag); e != nil {
			rows.Close()
			return nil, e
		}
		v["tags"] = append(v["tags"], tag)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for key, values := range v {
		limit := 100
		if key == "questions" {
			limit = 20
		}
		seen := map[string]bool{}
		out := []string{}
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				out = append(out, boundedKnowledgeText(value, 200))
				if len(out) == limit {
					break
				}
			}
		}
		v[key] = out
	}
	return v, nil
}
