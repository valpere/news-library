package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type ConnectionMember struct {
	ItemID           int64
	Title, URL, Role string
}

// ConnectionNote is the one current note; editing replaces it (no revisions).
type ConnectionNote struct {
	Author, Model, PromptVersion string
	ID                           int64
	Title, Category, Body        string
	Markdown                     string
	Tags, Questions              []string
	Members                      []ConnectionMember
}
type ConnectionSummary struct {
	ID              int64
	Title, Category string
}

func connectionList(values []string, maxCount, maxLength int) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxLength || strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("некоректні метадані нотатки")
		}
		key := strings.ToLower(v)
		if !seen[key] {
			out = append(out, v)
			seen[key] = true
		}
	}
	if len(out) > maxCount {
		return nil, errors.New("забагато метаданих нотатки")
	}
	return out, nil
}
func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
func connectionMarkdown(n ConnectionNote) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema: news-library/connection-v1\ntype: connection\ntitle: %s\ncategory: %s\ntags: %s\nquestions: %s\nauthor: %s\nmodel: %s\nprompt_version: %s\nsources:\n", jsonString(n.Title), jsonString(n.Category), jsonString(n.Tags), jsonString(n.Questions), jsonString(n.Author), jsonString(n.Model), jsonString(n.PromptVersion))
	for _, m := range n.Members {
		fmt.Fprintf(&b, "  - item_id: %d\n", m.ItemID)
		fmt.Fprintf(&b, "    title: %s\n    url: %s\n    role: %s\n", jsonString(m.Title), jsonString(m.URL), jsonString(m.Role))
	}
	fmt.Fprintf(&b, "---\n\n# %s\n\n%s\n\n## Матеріали\n", n.Title, n.Body)
	for _, m := range n.Members {
		fmt.Fprintf(&b, "\n- [Матеріал #%d](/items/%d) — %s\n", m.ItemID, m.ItemID, m.Role)
	}
	return b.String()
}

// SaveConnection stores the note as its one current state: a new note is inserted, an
// existing one (n.ID != 0) is replaced together with its members and file. The
// last save wins; there are no revisions.
func (s *Store) SaveConnection(n ConnectionNote) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n.Author, n.Model, n.PromptVersion = "user", "", ""
	id, replaced, err := s.saveConnectionTx(tx, n)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	s.removeUnusedFiles(map[string]bool{replaced: replaced != ""}) // best effort
	return id, nil
}

// saveConnectionTx returns the note id and the file path it replaced ("" for a new note).
func (s *Store) saveConnectionTx(tx *sql.Tx, n ConnectionNote) (int64, string, error) {
	n.Title = strings.TrimSpace(n.Title)
	n.Category = strings.TrimSpace(n.Category)
	n.Body = strings.TrimSpace(n.Body)
	for _, v := range []struct {
		text string
		max  int
	}{{n.Title, 200}, {n.Category, 120}, {n.Body, 30000}} {
		if !utf8.ValidString(v.text) || utf8.RuneCountInString(v.text) > v.max {
			return 0, "", errors.New("текст нотатки завеликий або некоректний")
		}
	}
	if n.Title == "" || n.Body == "" || strings.ContainsAny(n.Title+n.Category, "\r\n") {
		return 0, "", errors.New("потрібні назва та зміст нотатки")
	}
	var err error
	n.Tags, err = connectionList(n.Tags, 20, 80)
	if err != nil {
		return 0, "", err
	}
	n.Questions, err = connectionList(n.Questions, 20, 300)
	if err != nil {
		return 0, "", err
	}
	if len(n.Members) < 2 || len(n.Members) > 20 {
		return 0, "", errors.New("оберіть від 2 до 20 різних матеріалів")
	}
	seen := map[int64]bool{}
	for i := range n.Members {
		m := &n.Members[i]
		m.Role = strings.TrimSpace(m.Role)
		if m.ItemID <= 0 || seen[m.ItemID] || m.Role == "" || !utf8.ValidString(m.Role) || utf8.RuneCountInString(m.Role) > 1000 || strings.ContainsAny(m.Role, "\r\n") {
			return 0, "", errors.New("кожному різному матеріалу потрібне коротке пояснення ролі")
		}
		seen[m.ItemID] = true
		err = tx.QueryRow(`SELECT v.title,i.original_url FROM texts v JOIN items i ON i.id=v.item_id WHERE v.item_id=?`, m.ItemID).Scan(&m.Title, &m.URL)
		if err != nil {
			return 0, "", errors.New("матеріал не існує або ще не має збереженого тексту")
		}
	}
	var replaced string
	if n.ID != 0 {
		if err = tx.QueryRow(`SELECT markdown_path FROM connection_notes WHERE id=?`, n.ID).Scan(&replaced); err != nil {
			return 0, "", err
		}
	}
	n.Markdown = connectionMarkdown(n)
	path, err := s.Files.Put("digests", []byte(n.Markdown))
	if err != nil {
		return 0, "", err
	}
	search := strings.ToLower(n.Title + " " + n.Category + " " + strings.Join(n.Tags, " ") + " " + strings.Join(n.Questions, " ") + " " + n.Body)
	if n.ID == 0 {
		result, e := tx.Exec(`INSERT INTO connection_notes(title,category,tags_json,questions_json,body,search_text,markdown_path,hash,author,model,prompt_version) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			n.Title, n.Category, jsonString(n.Tags), jsonString(n.Questions), n.Body, search, path, textHash(n.Markdown), n.Author, n.Model, n.PromptVersion)
		if e != nil {
			return 0, "", e
		}
		if n.ID, err = result.LastInsertId(); err != nil {
			return 0, "", err
		}
	} else {
		if _, err = tx.Exec(`UPDATE connection_notes SET title=?,category=?,tags_json=?,questions_json=?,body=?,search_text=?,markdown_path=?,hash=?,author=?,model=?,prompt_version=? WHERE id=?`,
			n.Title, n.Category, jsonString(n.Tags), jsonString(n.Questions), n.Body, search, path, textHash(n.Markdown), n.Author, n.Model, n.PromptVersion, n.ID); err != nil {
			return 0, "", err
		}
		if _, err = tx.Exec(`DELETE FROM connection_members WHERE note_id=?`, n.ID); err != nil {
			return 0, "", err
		}
	}
	for _, m := range n.Members {
		if _, err = tx.Exec("INSERT INTO connection_members(note_id,item_id,role) VALUES(?,?,?)", n.ID, m.ItemID, m.Role); err != nil {
			return 0, "", err
		}
	}
	return n.ID, replaced, nil
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

// loadConnection reads the note; withFile also loads and verifies its Markdown file.
func (s *Store) loadConnection(q rowQuerier, id int64, withFile bool) (ConnectionNote, error) {
	var n ConnectionNote
	var tags, questions, path, hash string
	err := q.QueryRow(`SELECT id,title,category,tags_json,questions_json,body,markdown_path,hash,author,model,prompt_version FROM connection_notes WHERE id=?`, id).Scan(&n.ID, &n.Title, &n.Category, &tags, &questions, &n.Body, &path, &hash, &n.Author, &n.Model, &n.PromptVersion)
	if err != nil {
		return n, err
	}
	if err = json.Unmarshal([]byte(tags), &n.Tags); err != nil {
		return n, err
	}
	if err = json.Unmarshal([]byte(questions), &n.Questions); err != nil {
		return n, err
	}
	if withFile {
		data, err := s.Files.Read(path, hash)
		if err != nil {
			return n, err
		}
		n.Markdown = string(data)
	}
	rows, err := q.Query(`SELECT m.item_id,v.title,i.original_url,m.role FROM connection_members m JOIN texts v ON v.item_id=m.item_id JOIN items i ON i.id=m.item_id WHERE m.note_id=? ORDER BY m.item_id`, id)
	if err != nil {
		return n, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ConnectionMember
		if err = rows.Scan(&m.ItemID, &m.Title, &m.URL, &m.Role); err != nil {
			return n, err
		}
		n.Members = append(n.Members, m)
	}
	return n, rows.Err()
}
func (s *Store) Connection(id int64) (ConnectionNote, error) { return s.loadConnection(s.DB, id, true) }

// Connections lists (and with query searches) the current notes; item!=0 keeps the
// notes that mention that material (its backlinks).
func (s *Store) Connections(query string, item int64) ([]ConnectionSummary, error) {
	rows, err := s.DB.Query(`SELECT n.id,n.title,n.category FROM connection_notes n WHERE (?=0 OR EXISTS(SELECT 1 FROM connection_members WHERE note_id=n.id AND item_id=?)) AND (?='' OR instr(n.search_text,?)>0) ORDER BY n.id DESC LIMIT 100`, item, item, query, strings.ToLower(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConnectionSummary{}
	for rows.Next() {
		var n ConnectionSummary
		if err = rows.Scan(&n.ID, &n.Title, &n.Category); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// removeFromNotes takes the material out of every note that mentions it: a note left
// with fewer than two materials is deleted, the others get their file rebuilt. It
// returns the files that are no longer used (to be removed after the commit).
func (s *Store) removeFromNotes(tx *sql.Tx, itemID int64) (map[string]bool, error) {
	paths := map[string]bool{}
	ids, err := queryIDs(tx, `SELECT note_id FROM connection_members WHERE item_id=?`, itemID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		// The file is rebuilt from the database fields, so a damaged old file cannot block this.
		n, err := s.loadConnection(tx, id, false)
		if err != nil {
			return nil, err
		}
		var oldPath string
		if err = tx.QueryRow(`SELECT markdown_path FROM connection_notes WHERE id=?`, id).Scan(&oldPath); err != nil {
			return nil, err
		}
		paths[oldPath] = true
		kept := n.Members[:0]
		for _, m := range n.Members {
			if m.ItemID != itemID {
				kept = append(kept, m)
			}
		}
		if len(kept) < 2 {
			if _, err = tx.Exec(`DELETE FROM connection_members WHERE note_id=?`, id); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(`DELETE FROM connection_notes WHERE id=?`, id); err != nil {
				return nil, err
			}
			continue
		}
		n.Members = kept
		n.Markdown = connectionMarkdown(n)
		path, err := s.Files.Put("digests", []byte(n.Markdown))
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE connection_notes SET markdown_path=?,hash=? WHERE id=?`, path, textHash(n.Markdown), id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`DELETE FROM connection_members WHERE note_id=? AND item_id=?`, id, itemID); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// Suggestions reuse the existing vocabulary without rewriting article tags.
func (s *Store) ConnectionVocabulary() (map[string][]string, error) {
	out := map[string][]string{"tags": {}, "categories": {}, "questions": {}}
	for _, kind := range []string{"tags", "categories", "questions"} {
		query := `SELECT DISTINCT category FROM connection_notes WHERE category<>'' ORDER BY category LIMIT 200`
		if kind != "categories" {
			column := "tags_json"
			if kind == "questions" {
				column = "questions_json"
			}
			query = `SELECT DISTINCT j.value FROM connection_notes v,json_each(v.` + column + `) j`
			if kind == "tags" {
				query += ` UNION SELECT DISTINCT tag FROM item_tags`
			}
			query += ` ORDER BY 1 LIMIT 200`
		}
		rows, err := s.DB.Query(query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				rows.Close()
				return nil, err
			}
			out[kind] = append(out[kind], value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) ConnectionArticles() ([]Item, error) {
	rows, err := s.DB.Query(itemSelect + ` WHERE EXISTS(SELECT 1 FROM texts WHERE texts.item_id=i.id) ORDER BY i.added_at DESC,i.id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		item, e := scanItem(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
