package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }
type Item struct {
	ID                                                        int64
	OriginalURL, URL, Title, AddedAt, Placement, State, Error string
	Read                                                      bool
	Selected                                                  sql.NullInt64
	Score                                                     sql.NullInt64
	Tags                                                      string
}
type Snapshot struct {
	ID, ItemID                                                               int64
	Text, Title, Hash, RetrievedAt, Origin, Extractor, Completeness, Warning string
}
type RatingEvent struct {
	ID, ItemID, Score         int64
	RecordedAt                string
	AssessedAt                sql.NullString
	SourceVersionID, DigestID sql.NullInt64
	Reason                    string
}
type Job struct {
	ID, ItemID      int64
	URL, Token      string
	Attempts        int
	SelectedAtClaim sql.NullInt64
}
type Filters struct {
	Query, Placement, Rating, Read, State, Sort, Direction string
	Tags                                                   []string
}

func Open(path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, errors.New("database schema is newer than this application")
	}
	tx, err := db.Begin()
	if err == nil {
		_, err = tx.Exec(schema)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func nowUTC() string          { return time.Now().UTC().Format(time.RFC3339Nano) }

func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("потрібен абсолютний http/https URL без облікових даних")
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	// Preserve query order and encoding; only remove known tracking keys.
	var parts []string
	for _, part := range strings.Split(u.RawQuery, "&") {
		if part == "" {
			continue
		}
		key, err := url.QueryUnescape(strings.SplitN(part, "=", 2)[0])
		if err != nil {
			return "", err
		}
		switch strings.ToLower(key) {
		case "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "utm_id", "fbclid", "gclid":
			continue
		}
		parts = append(parts, part)
	}
	u.RawQuery = strings.Join(parts, "&")
	return u.String(), nil
}
func (s *Store) AddURL(raw string) (int64, error) {
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec("INSERT INTO items(original_url,canonical_url,added_at) VALUES(?,?,?) ON CONFLICT(canonical_url) DO NOTHING", strings.TrimSpace(raw), canonical, nowUTC())
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	var id int64
	if err = tx.QueryRow("SELECT id FROM items WHERE canonical_url=?", canonical).Scan(&id); err != nil {
		return 0, err
	}
	if _, err = tx.Exec("INSERT INTO source_aliases(url,item_id) VALUES(?,?) ON CONFLICT(url) DO NOTHING", strings.TrimSpace(raw), id); err != nil {
		return 0, err
	}
	if count == 1 {
		if _, err = tx.Exec("INSERT INTO jobs(item_id) VALUES(?)", id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec("INSERT INTO item_fts(rowid,title,text,url) VALUES(?,'','',?)", id, canonical); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

var ErrLeaseLost = errors.New("job lease lost")

func (s *Store) Claim(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (*Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// A crash on the final attempt must terminate instead of retrying forever.
	if _, err = tx.ExecContext(ctx, `UPDATE items SET state='needs_input',error='fetch attempts exhausted after restart' WHERE selected_snapshot IS NULL AND id IN (SELECT item_id FROM jobs WHERE state='running' AND lease_until<=? AND attempts>=?)`, now.UnixMilli(), maxAttempts); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',error='fetch attempts exhausted after restart' WHERE state='running' AND lease_until<=? AND attempts>=?`, now.UnixMilli(), maxAttempts); err != nil {
		return nil, err
	}
	var j Job
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.item_id,i.canonical_url,j.attempts,i.selected_snapshot FROM jobs j JOIN items i ON i.id=j.item_id WHERE (j.state IN ('queued','retry_wait') AND j.next_attempt<=?) OR (j.state='running' AND j.lease_until<=?) ORDER BY j.id LIMIT 1`, now.UnixMilli(), now.UnixMilli()).Scan(&j.ID, &j.ItemID, &j.URL, &j.Attempts, &j.SelectedAtClaim)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	j.Token = rand.Text()
	j.Attempts++
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state='running',token=?,attempts=?,lease_until=? WHERE id=?", j.Token, j.Attempts, now.Add(lease).UnixMilli(), j.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE items SET state='fetching',error='' WHERE id=? AND selected_snapshot IS NULL", j.ItemID); err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}
func (s *Store) Renew(j Job, now time.Time, lease time.Duration) error {
	r, err := s.DB.Exec("UPDATE jobs SET lease_until=? WHERE id=? AND state='running' AND token=? AND lease_until>?", now.Add(lease).UnixMilli(), j.ID, j.Token, now.UnixMilli())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func leaseValid(tx *sql.Tx, j Job) error {
	var n int
	err := tx.QueryRow("SELECT count(*) FROM jobs WHERE id=? AND item_id=? AND state='running' AND token=? AND lease_until>?", j.ID, j.ItemID, j.Token, time.Now().UnixMilli()).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func indexSnapshot(tx *sql.Tx, id int64, title, text string) error {
	_, err := tx.Exec("UPDATE item_fts SET title=?,text=? WHERE rowid=?", title, text, id)
	return err
}
func insertSnapshot(tx *sql.Tx, id int64, jobID any, snap Snapshot) (int64, error) {
	sum := sha256.Sum256([]byte(snap.Text))
	r, err := tx.Exec("INSERT INTO source_versions(item_id,job_id,text,title,hash,retrieved_at,origin,extractor,completeness,warning) VALUES(?,?,?,?,?,?,?,?,?,?)", id, jobID, snap.Text, snap.Title, fmt.Sprintf("%x", sum), nowUTC(), snap.Origin, snap.Extractor, snap.Completeness, snap.Warning)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func (s *Store) Complete(j Job, snap Snapshot) error {
	if strings.TrimSpace(snap.Text) == "" {
		return errors.New("empty snapshot")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseValid(tx, j); err != nil {
		return err
	}
	sid, err := insertSnapshot(tx, j.ItemID, j.ID, snap)
	if err != nil {
		return err
	}
	// Manual input wins over an already-running fetch.
	var origin string
	var selected sql.NullInt64
	if err = tx.QueryRow("SELECT coalesce((SELECT origin FROM source_versions WHERE id=selected_snapshot),''),selected_snapshot FROM items WHERE id=?", j.ItemID).Scan(&origin, &selected); err != nil {
		return err
	}
	if origin != "manual" || selected == j.SelectedAtClaim {
		if _, err = tx.Exec("UPDATE items SET selected_snapshot=?,title=?,state='ready',error='' WHERE id=?", sid, snap.Title, j.ItemID); err != nil {
			return err
		}
		if err = indexSnapshot(tx, j.ItemID, snap.Title, snap.Text); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE jobs SET state='done',lease_until=0,error='' WHERE id=?", j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Fail(j Job, message string, retry bool, maxAttempts int, now time.Time, backoff time.Duration) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseValid(tx, j); err != nil {
		return err
	}
	state, itemState := "failed", "needs_input"
	if retry && j.Attempts < maxAttempts {
		state, itemState = "retry_wait", "retry_wait"
	}
	if _, err = tx.Exec("UPDATE jobs SET state=?,error=?,next_attempt=?,lease_until=0 WHERE id=?", state, message, now.Add(backoff).UnixMilli(), j.ID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE items SET state=?,error=? WHERE id=? AND selected_snapshot IS NULL", itemState, message, j.ItemID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Retry(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec("INSERT INTO jobs(item_id) SELECT id FROM items WHERE id=? AND NOT EXISTS(SELECT 1 FROM jobs WHERE item_id=? AND state IN ('queued','running','retry_wait'))", id, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("запис відсутній або отримання вже активне")
	}
	if _, err = tx.Exec("UPDATE items SET state='queued',error='' WHERE id=? AND selected_snapshot IS NULL", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Manual(id int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("порожній текст")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var title string
	if err = tx.QueryRow("SELECT title FROM items WHERE id=?", id).Scan(&title); err != nil {
		return err
	}
	sid, err := insertSnapshot(tx, id, nil, Snapshot{Text: text, Title: title, Origin: "manual", Extractor: "manual-v1", Completeness: "unknown", Warning: "Повнота ручного тексту невідома"})
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE items SET selected_snapshot=?,state='ready',error='' WHERE id=?", sid, id); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE jobs SET state='cancelled' WHERE item_id=? AND state IN ('queued','retry_wait')", id); err != nil {
		return err
	}
	if err = indexSnapshot(tx, id, title, text); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Rate(id, score int64, source *int64, assessed *time.Time, reason string) error {
	if score < 0 {
		return errors.New("оцінка має бути невід’ємним цілим")
	}
	var at any
	if assessed != nil {
		at = assessed.UTC().Format(time.RFC3339Nano)
	}
	var sid any
	if source != nil {
		sid = *source
	}
	_, err := s.DB.Exec("INSERT INTO rating_events(item_id,score,recorded_at,assessed_at,source_version_id,reason) VALUES(?,?,?,?,?,?)", id, score, nowUTC(), at, sid, reason)
	return err
}
func (s *Store) SetPlacement(id int64, value string) error {
	if value != "inbox" && value != "library" && value != "deferred" {
		return errors.New("невідоме розміщення")
	}
	return s.update("UPDATE items SET placement=? WHERE id=?", value, id)
}
func (s *Store) SetRead(id int64, value bool) error {
	return s.update("UPDATE items SET is_read=? WHERE id=?", value, id)
}
func (s *Store) update(query string, args ...any) error {
	r, e := s.DB.Exec(query, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) SetTags(id int64, tags []string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM items WHERE id=?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.Exec("DELETE FROM item_tags WHERE item_id=? AND origin='human'", id); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err = tx.Exec("INSERT INTO item_tags(item_id,tag) VALUES(?,?) ON CONFLICT DO NOTHING", id, tag); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const itemSelect = `SELECT i.id,i.original_url,i.canonical_url,i.title,i.added_at,i.placement,i.state,i.error,i.is_read,i.selected_snapshot,(SELECT score FROM rating_events WHERE item_id=i.id ORDER BY id DESC LIMIT 1),(SELECT group_concat(tag,', ') FROM (SELECT tag FROM item_tags WHERE item_id=i.id ORDER BY tag)) FROM items i`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var i Item
	var tags sql.NullString
	err := row.Scan(&i.ID, &i.OriginalURL, &i.URL, &i.Title, &i.AddedAt, &i.Placement, &i.State, &i.Error, &i.Read, &i.Selected, &i.Score, &tags)
	i.Tags = tags.String
	return i, err
}
func (s *Store) Item(id int64) (Item, error) {
	return scanItem(s.DB.QueryRow(itemSelect+" WHERE i.id=?", id))
}
func (s *Store) Snapshots(id int64) ([]Snapshot, error) {
	rows, err := s.DB.Query("SELECT id,item_id,text,title,hash,retrieved_at,origin,extractor,completeness,warning FROM source_versions WHERE item_id=? ORDER BY id DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Snapshot
	for rows.Next() {
		var v Snapshot
		if err = rows.Scan(&v.ID, &v.ItemID, &v.Text, &v.Title, &v.Hash, &v.RetrievedAt, &v.Origin, &v.Extractor, &v.Completeness, &v.Warning); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Store) Ratings(id int64) ([]RatingEvent, error) {
	rows, err := s.DB.Query("SELECT id,item_id,score,recorded_at,assessed_at,source_version_id,digest_id,reason FROM rating_events WHERE item_id=? ORDER BY id DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RatingEvent
	for rows.Next() {
		var v RatingEvent
		if err = rows.Scan(&v.ID, &v.ItemID, &v.Score, &v.RecordedAt, &v.AssessedAt, &v.SourceVersionID, &v.DigestID, &v.Reason); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Store) Search(f Filters) ([]Item, error) {
	query := itemSelect + " WHERE 1=1"
	var args []any
	if f.Query != "" {
		var terms []string
		for _, word := range strings.Fields(f.Query) {
			terms = append(terms, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
		}
		query += " AND i.id IN (SELECT rowid FROM item_fts WHERE item_fts MATCH ?)"
		args = append(args, strings.Join(terms, " AND "))
	}
	for _, pair := range []struct{ v, column string }{{f.Placement, "i.placement"}, {f.State, "i.state"}} {
		if pair.v != "" {
			query += " AND " + pair.column + "=?"
			args = append(args, pair.v)
		}
	}
	if f.Read != "" {
		if f.Read != "0" && f.Read != "1" {
			return nil, errors.New("invalid read filter")
		}
		query += " AND i.is_read=?"
		args = append(args, f.Read)
	}
	scoreExpr := "(SELECT score FROM rating_events WHERE item_id=i.id ORDER BY id DESC LIMIT 1)"
	if f.Rating == "unrated" {
		query += " AND " + scoreExpr + " IS NULL"
	} else if f.Rating != "" {
		query += " AND " + scoreExpr + "=?"
		args = append(args, f.Rating)
	}
	if len(f.Tags) > 0 {
		query += " AND EXISTS(SELECT 1 FROM item_tags WHERE item_id=i.id AND tag IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.Tags)), ",") + "))"
		for _, tag := range f.Tags {
			args = append(args, tag)
		}
	}
	direction := "DESC"
	if f.Direction == "asc" {
		direction = "ASC"
	}
	switch f.Sort {
	case "rating":
		query += " ORDER BY " + scoreExpr + " IS NULL," + scoreExpr + " " + direction + ",i.id ASC"
	case "relevance":
		if f.Query == "" {
			return nil, errors.New("релевантність потребує запиту")
		}
		query += " ORDER BY (SELECT rank FROM item_fts WHERE rowid=i.id AND item_fts MATCH ?) ASC,i.id ASC"
		args = append(args, strings.Join(func() []string {
			var a []string
			for _, w := range strings.Fields(f.Query) {
				a = append(a, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
			}
			return a
		}(), " AND "))
	default:
		query += " ORDER BY i.added_at " + direction + ",i.id ASC"
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		i, e := scanItem(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, i)
	}
	return items, rows.Err()
}
