package library

import (
	"fmt"
	"net/http"
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
	k, err := w.Store.Knowledge(id, sid)
	if err != nil || k == nil || k.FileError != "" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	rw.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="article-%d-knowledge-%d.md"`, id, k.ID))
	fmt.Fprint(rw, k.Markdown)
}
