package library

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/yuin/goldmark/v2/parser"
	mdhtml "github.com/yuin/goldmark/v2/renderer/html"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

func (w *Web) connectionListPage(rw http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	item := int64(0)
	if raw := r.URL.Query().Get("item"); raw != "" {
		var err error
		item, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || item <= 0 {
			http.Error(rw, "некоректний матеріал", 400)
			return
		}
	}
	notes, err := w.Store.Connections(query, item)
	if err != nil {
		http.Error(rw, "нотатки недоступні", http.StatusServiceUnavailable)
		return
	}
	w.render(rw, "connections", pageData{Connections: notes, ConnectionQuery: query})
}
func (w *Web) connectionPage(rw http.ResponseWriter, r *http.Request) {
	if format := r.PathValue("format"); format != "" && format != "markdown" {
		http.NotFound(rw, r)
		return
	}
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	note, err := w.Store.Connection(id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	if r.PathValue("format") == "markdown" {
		rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		rw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="connection-%d.md"`, note.ID))
		fmt.Fprint(rw, note.Markdown)
		return
	}
	var body bytes.Buffer
	if err = mdhtml.New().Render(&body, []byte(note.Body), parser.New().Parse([]byte(note.Body))); err != nil {
		http.Error(rw, "зміст недоступний", http.StatusServiceUnavailable)
		return
	}
	w.render(rw, "connection", pageData{Connection: &note, ConnectionHTML: template.HTML(body.String())})
}
func (w *Web) connectionForm(rw http.ResponseWriter, r *http.Request) {
	n := ConnectionNote{}
	if r.PathValue("id") != "" {
		id, err := pathID(r)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		n, err = w.Store.Connection(id)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
	}
	if n.ID == 0 && r.URL.Query().Get("item") != "" {
		id, err := strconv.ParseInt(r.URL.Query().Get("item"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(rw, "некоректний матеріал", 400)
			return
		}
		item, err := w.Store.Item(id)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		if item.HasText {
			n.Members = append(n.Members, ConnectionMember{ItemID: id, Title: item.Title, URL: item.URL})
		}
	}
	w.renderConnectionForm(rw, r, n, "", 200)
}
func parseConnectionMembers(raw string) ([]ConnectionMember, error) {
	out := []ConnectionMember{}
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			return nil, errors.New("матеріали: ID | роль")
		}
		m := ConnectionMember{Role: strings.TrimSpace(parts[1])}
		id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("некоректний ID матеріалу")
		}
		m.ItemID = id
		out = append(out, m)
	}
	return out, nil
}
func (w *Web) saveConnection(rw http.ResponseWriter, r *http.Request) {
	n := ConnectionNote{Title: r.PostForm.Get("title"), Category: r.PostForm.Get("category"), Body: r.PostForm.Get("body"), Tags: strings.Split(r.PostForm.Get("tags"), ","), Questions: strings.Split(r.PostForm.Get("questions"), "\n")}
	var err error
	if r.PathValue("id") != "" {
		n.ID, err = pathID(r)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
	}
	items, roles := r.PostForm["item"], r.PostForm["role"]
	if len(items) != len(roles) || len(items) > 24 {
		http.Error(rw, "некоректні матеріали", 400)
		return
	}
	omit := map[string]bool{}
	for _, v := range r.PostForm["omit"] {
		omit[v] = true
	}
	for i, value := range items {
		if value == "" || omit[strconv.Itoa(i)] {
			continue
		}
		row, e := parseConnectionMembers(value + " | " + roles[i])
		if e != nil {
			err = e
			break
		}
		n.Members = append(n.Members, row...)
	}
	if err == nil {
		var saved int64
		saved, err = w.Store.SaveConnection(n)
		if err == nil {
			n.ID = saved
		}
	}
	if err != nil {
		// Preserve the user's draft for validation errors.
		w.renderConnectionForm(rw, r, n, err.Error(), 400)
		return
	}
	http.Redirect(rw, r, "/connections/"+strconv.FormatInt(n.ID, 10), http.StatusSeeOther)
}

func (w *Web) renderConnectionForm(rw http.ResponseWriter, r *http.Request, n ConnectionNote, message string, status int) {
	choices, err := w.Store.ConnectionArticles()
	if err != nil {
		http.Error(rw, "матеріали недоступні", http.StatusServiceUnavailable)
		return
	}
	ready := []Item{}
	for _, item := range choices {
		if item.HasText {
			ready = append(ready, item)
		}
	}
	if n.ID == 0 && len(n.Members) == 0 && r.URL.Query().Get("item") != "" {
		id, _ := strconv.ParseInt(r.URL.Query().Get("item"), 10, 64)
		item, e := w.Store.Item(id)
		if e == nil && item.HasText {
			n.Members = append(n.Members, ConnectionMember{ItemID: id, Title: item.Title, URL: item.URL})
		}
	}
	for i := range n.Members {
		if n.Members[i].Title == "" {
			item, e := w.Store.Item(n.Members[i].ItemID)
			if e == nil {
				n.Members[i].Title = item.Title
				n.Members[i].URL = item.URL
			}
		}
	}
	slots := []int{}
	for i := len(n.Members); i < len(n.Members)+4 && i < 20; i++ {
		slots = append(slots, i)
	}
	vocabulary, err := w.Store.ConnectionVocabulary()
	if err != nil {
		http.Error(rw, "словник недоступний", http.StatusServiceUnavailable)
		return
	}
	w.renderStatus(rw, "connectionForm", pageData{Connection: &n, ConnectionChoices: ready, ConnectionSlots: slots, ConnectionError: message, ConnectionVocabulary: vocabulary}, status)
}
