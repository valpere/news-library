package library

import (
	"fmt"
	"net/http"
	"strconv"
)

func (w *Web) exportKnowledge(rw http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	item, err := w.Store.Item(id)
	if err != nil || !item.Selected.Valid {
		http.NotFound(rw, r)
		return
	}
	sid := item.Selected.Int64
	if raw := r.URL.Query().Get("source"); raw != "" {
		sid, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || sid <= 0 {
			http.Error(rw, "некоректний текст", 400)
			return
		}
	}
	k, err := w.Store.Knowledge(id, sid)
	if err != nil || k == nil || k.FileError != "" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	rw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="article-%d-knowledge-%d.md"`, id, k.ID))
	fmt.Fprint(rw, k.Markdown)
}
