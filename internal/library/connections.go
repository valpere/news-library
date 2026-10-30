package library

import (
	"crypto/rand"
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
type ConnectionNote struct {
	Author, Model, PromptVersion                         string
	ID, VersionID, Revision                              int64
	StableID, Title, Category, Body, CreatedAt, Markdown string
	Tags, Questions                                      []string
	Members                                              []ConnectionMember
}
type ConnectionSummary struct {
	ID, VersionID, Revision    int64
	Title, Category, CreatedAt string
}

var ErrConnectionConflict = errors.New("нотатка змінилася; відкрийте актуальну версію перед збереженням")

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
	fmt.Fprintf(&b, "---\nschema: news-library/connection-v1\ntype: connection\nid: %s\nrevision: %d\ntitle: %s\ncategory: %s\ntags: %s\nquestions: %s\ncreated_at: %s\nauthor: %s\nmodel: %s\nprompt_version: %s\nsources:\n", jsonString(n.StableID), n.Revision, jsonString(n.Title), jsonString(n.Category), jsonString(n.Tags), jsonString(n.Questions), jsonString(n.CreatedAt), jsonString(n.Author), jsonString(n.Model), jsonString(n.PromptVersion))
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

// SaveConnection publishes a new immutable revision, with optimistic concurrency
// and references to the exact sources used. It never changes an article.
func (s *Store) SaveConnection(n ConnectionNote, expectedVersion int64) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n.Author, n.Model, n.PromptVersion = "user", "", ""
	id, err := s.saveConnectionTx(tx, n, expectedVersion)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Store) saveConnectionTx(tx *sql.Tx, n ConnectionNote, expectedVersion int64) (int64, error) {
	n.Title = strings.TrimSpace(n.Title)
	n.Category = strings.TrimSpace(n.Category)
	n.Body = strings.TrimSpace(n.Body)
	for _, v := range []struct {
		text string
		max  int
	}{{n.Title, 200}, {n.Category, 120}, {n.Body, 30000}} {
		if !utf8.ValidString(v.text) || utf8.RuneCountInString(v.text) > v.max {
			return 0, errors.New("текст нотатки завеликий або некоректний")
		}
	}
	if n.Title == "" || n.Body == "" || strings.ContainsAny(n.Title+n.Category, "\r\n") {
		return 0, errors.New("потрібні назва та зміст нотатки")
	}
	var err error
	n.Tags, err = connectionList(n.Tags, 20, 80)
	if err != nil {
		return 0, err
	}
	n.Questions, err = connectionList(n.Questions, 20, 300)
	if err != nil {
		return 0, err
	}
	if len(n.Members) < 2 || len(n.Members) > 20 {
		return 0, errors.New("оберіть від 2 до 20 різних матеріалів")
	}
	n.CreatedAt = nowUTC()
	n.Revision = 1
	if n.ID == 0 {
		if expectedVersion != 0 {
			return 0, ErrConnectionConflict
		}
		n.StableID = rand.Text()
		result, e := tx.Exec("INSERT INTO connection_notes(stable_id,created_at) VALUES(?,?)", n.StableID, n.CreatedAt)
		if e != nil {
			return 0, e
		}
		n.ID, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else {
		var current int64
		err = tx.QueryRow(`SELECT v.id,v.revision,c.stable_id FROM connection_versions v JOIN connection_notes c ON c.id=v.note_id WHERE v.note_id=? ORDER BY v.revision DESC LIMIT 1`, n.ID).Scan(&current, &n.Revision, &n.StableID)
		if err != nil {
			return 0, err
		}
		if current != expectedVersion {
			return 0, ErrConnectionConflict
		}
		n.Revision++
	}
	seen := map[int64]bool{}
	for i := range n.Members {
		m := &n.Members[i]
		m.Role = strings.TrimSpace(m.Role)
		if m.ItemID <= 0 || seen[m.ItemID] || m.Role == "" || !utf8.ValidString(m.Role) || utf8.RuneCountInString(m.Role) > 1000 || strings.ContainsAny(m.Role, "\r\n") {
			return 0, errors.New("кожному різному матеріалу потрібне коротке пояснення ролі")
		}
		seen[m.ItemID] = true
		err = tx.QueryRow(`SELECT v.title,i.original_url FROM texts v JOIN items i ON i.id=v.item_id WHERE v.item_id=?`, m.ItemID).Scan(&m.Title, &m.URL)
		if err != nil {
			return 0, errors.New("матеріал не існує або ще не має збереженого тексту")
		}
	}
	n.Markdown = connectionMarkdown(n)
	path, err := s.Files.Put("digests", []byte(n.Markdown))
	if err != nil {
		return 0, err
	}
	result, err := tx.Exec(`INSERT INTO connection_versions(note_id,revision,title,category,tags_json,questions_json,body,search_text,markdown_path,hash,created_at,author,model,prompt_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, n.ID, n.Revision, n.Title, n.Category, jsonString(n.Tags), jsonString(n.Questions), n.Body, strings.ToLower(n.Title+" "+n.Category+" "+strings.Join(n.Tags, " ")+" "+strings.Join(n.Questions, " ")+" "+n.Body), path, textHash(n.Markdown), n.CreatedAt, n.Author, n.Model, n.PromptVersion)
	if err != nil {
		return 0, err
	}
	vid, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, m := range n.Members {
		if _, err = tx.Exec("INSERT INTO connection_members(version_id,item_id,role) VALUES(?,?,?)", vid, m.ItemID, m.Role); err != nil {
			return 0, err
		}
	}
	return n.ID, nil
}
func (s *Store) Connection(id, version int64) (ConnectionNote, error) {
	var n ConnectionNote
	var tags, questions, path, hash string
	err := s.DB.QueryRow(`SELECT c.id,c.stable_id,v.id,v.revision,v.title,v.category,v.tags_json,v.questions_json,v.body,v.created_at,v.markdown_path,v.hash,v.author,v.model,v.prompt_version FROM connection_notes c JOIN connection_versions v ON v.note_id=c.id WHERE c.id=? AND (?=0 OR v.id=?) ORDER BY v.revision DESC LIMIT 1`, id, version, version).Scan(&n.ID, &n.StableID, &n.VersionID, &n.Revision, &n.Title, &n.Category, &tags, &questions, &n.Body, &n.CreatedAt, &path, &hash, &n.Author, &n.Model, &n.PromptVersion)
	if err != nil {
		return n, err
	}
	if err = json.Unmarshal([]byte(tags), &n.Tags); err != nil {
		return n, err
	}
	if err = json.Unmarshal([]byte(questions), &n.Questions); err != nil {
		return n, err
	}
	data, err := s.Files.Read(path, hash)
	if err != nil {
		return n, err
	}
	n.Markdown = string(data)
	rows, err := s.DB.Query(`SELECT m.item_id,v.title,i.original_url,m.role FROM connection_members m JOIN texts v ON v.item_id=m.item_id JOIN items i ON i.id=m.item_id WHERE m.version_id=? ORDER BY m.item_id`, n.VersionID)
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

// item=0 lists/searches notes; a backlink lists only current revisions.
// History explicitly lists every revision of one note.
func (s *Store) Connections(query string, item, history int64) ([]ConnectionSummary, error) {
	rows, err := s.DB.Query(`SELECT v.note_id,v.id,v.revision,v.title,v.category,v.created_at FROM connection_versions v WHERE (?<>0 AND v.note_id=? OR ?=0 AND v.revision=(SELECT max(revision) FROM connection_versions WHERE note_id=v.note_id)) AND (?=0 OR EXISTS(SELECT 1 FROM connection_members WHERE version_id=v.id AND item_id=?)) AND (?='' OR instr(v.search_text,?)>0) ORDER BY v.id DESC LIMIT 100`, history, history, history, item, item, query, strings.ToLower(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConnectionSummary{}
	for rows.Next() {
		var n ConnectionSummary
		if err = rows.Scan(&n.ID, &n.VersionID, &n.Revision, &n.Title, &n.Category, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Suggestions reuse the existing vocabulary without rewriting article tags.
func (s *Store) ConnectionVocabulary() (map[string][]string, error) {
	out := map[string][]string{"tags": {}, "categories": {}, "questions": {}}
	for _, kind := range []string{"tags", "categories", "questions"} {
		query := `SELECT DISTINCT category FROM connection_versions v WHERE category<>'' AND revision=(SELECT max(revision) FROM connection_versions WHERE note_id=v.note_id) ORDER BY category LIMIT 200`
		if kind != "categories" {
			column := "tags_json"
			if kind == "questions" {
				column = "questions_json"
			}
			query = `SELECT DISTINCT j.value FROM connection_versions v,json_each(v.` + column + `) j WHERE revision=(SELECT max(revision) FROM connection_versions WHERE note_id=v.note_id)`
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
