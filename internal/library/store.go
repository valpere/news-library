package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed schema_v2.sql
var schemaV2 string

//go:embed schema_v4.sql
var schemaV4 string

//go:embed schema_v5.sql
var schemaV5 string

//go:embed schema_v6.sql
var schemaV6 string

//go:embed schema_v7.sql
var schemaV7 string

//go:embed schema_v8.sql
var schemaV8 string

//go:embed schema_v9.sql
var schemaV9 string

//go:embed schema_v10.sql
var schemaV10 string

//go:embed schema_v11.sql
var schemaV11 string

//go:embed schema_v12.sql
var schemaV12 string

//go:embed schema_v13.sql
var schemaV13 string

//go:embed schema_v14.sql
var schemaV14 string

//go:embed schema_v15.sql
var schemaV15 string

//go:embed schema_v16.sql
var schemaV16 string

//go:embed schema_v17.sql
var schemaV17 string

type Store struct {
	KnowledgeEnabled bool
	DB               *sql.DB
	DigestConfig     *DigestConfig
	Files            Files
	Rules            RuleSet
}
type Item struct {
	MatchSnippet                                              string
	MatchSnapshot                                             int64
	MatchStart, MatchEnd                                      int
	ID                                                        int64
	OriginalURL, URL, Title, AddedAt, Placement, State, Error string
	Read                                                      bool
	Selected                                                  sql.NullInt64
	Score                                                     sql.NullInt64
	ScoreScale                                                string
	Tags                                                      string
	FilterState                                               string
	Confidence                                                sql.NullFloat64
}
type Snapshot struct {
	ID, ItemID                                                               int64
	Text, Title, Hash, RetrievedAt, Origin, Extractor, Completeness, Warning string
	// UserConfirmed: the user vouched that this text is complete (overlay on the
	// extractor's value, which stays in source_versions). ConfirmedAt is its date.
	UserConfirmed bool
	ConfirmedAt   string
}
type RatingEvent struct {
	ID, ItemID, Score         int64
	RecordedAt                string
	AssessedAt                sql.NullString
	SourceVersionID, DigestID sql.NullInt64
	Reason                    string
	Scale                     string
}
type Job struct {
	ID, ItemID                                 int64
	URL, Token                                 string
	Attempts                                   int
	SelectedAtClaim                            sql.NullInt64
	Stage, ConfigVersion, Model, PromptVersion string
	SnapshotID                                 sql.NullInt64
	InputLimit                                 int
	Refresh                                    bool
	FilterOverride                             bool
	Interests                                  string
	ChunkLimit, ChunkMaxParts                  int
}
type Filters struct {
	Usefulness                                             string
	Category                                               string
	Query, Placement, Rating, Read, State, Sort, Direction string
	Filter, ConfidenceMin, ConfidenceMax                   string
	Tags                                                   []string
}

func Open(path string) (*Store, error) {
	if _, e := os.Lstat(filepath.Join(filepath.Dir(path), transferMarker)); !errors.Is(e, os.ErrNotExist) {
		return nil, errors.New("data transfer is incomplete")
	}
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
	if version > CurrentSchema {
		db.Close()
		return nil, errors.New("database schema is newer than this application")
	}
	if version > 0 && version < CurrentSchema {
		if err = backupBeforeMigration(db, path, version); err != nil {
			db.Close()
			return nil, err
		}
	}
	tx, err := db.Begin()
	if err == nil {
		if version == 0 {
			_, err = tx.Exec(schema)
		}
		if err == nil && version < 2 {
			_, err = tx.Exec(schemaV2)
		}
		if err == nil && version < 3 {
			err = migrateFiles(tx, Files{Root: filepath.Dir(path)})
		}
		if err == nil && version < 4 {
			_, err = tx.Exec(schemaV4)
		}
		if err == nil && version < 5 {
			_, err = tx.Exec(schemaV5)
		}
		if err == nil && version < 6 {
			_, err = tx.Exec(schemaV6)
		}
		if err == nil && version < 7 {
			_, err = tx.Exec(schemaV7)
		}
		if err == nil && version < 8 {
			_, err = tx.Exec(schemaV8)
		}
		if err == nil && version < 9 {
			_, err = tx.Exec(schemaV9)
		}
		if err == nil && version < 10 {
			_, err = tx.Exec(schemaV10)
		}
		if err == nil && version < 11 {
			_, err = tx.Exec(schemaV11)
		}
		if err == nil && version < 12 {
			_, err = tx.Exec(schemaV12)
		}
		if err == nil && version < 13 {
			_, err = tx.Exec(schemaV13)
		}
		if err == nil && version < 14 {
			_, err = tx.Exec(schemaV14)
		}
		if err == nil && version < 15 {
			_, err = tx.Exec(schemaV15)
		}
		if err == nil && version < 16 {
			_, err = tx.Exec(schemaV16)
		}
		if err == nil && version < 17 {
			_, err = tx.Exec(schemaV17)
		}
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
	return &Store{DB: db, Files: Files{Root: filepath.Dir(path)}, Rules: RuleSet{Threshold: 0.8, Rules: []FilterRule{}}}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func nowUTC() string          { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

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
	id, _, err := s.addURL(raw)
	return id, err
}
func (s *Store) addURL(raw string) (int64, bool, error) {
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return 0, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec("INSERT INTO items(original_url,canonical_url,added_at) VALUES(?,?,?) ON CONFLICT(canonical_url) DO NOTHING", strings.TrimSpace(raw), canonical, nowUTC())
	if err != nil {
		return 0, false, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	var id int64
	if err = tx.QueryRow("SELECT id FROM items WHERE canonical_url=?", canonical).Scan(&id); err != nil {
		return 0, false, err
	}
	if _, err = tx.Exec("INSERT INTO source_aliases(url,item_id) VALUES(?,?) ON CONFLICT(url) DO NOTHING", strings.TrimSpace(raw), id); err != nil {
		return 0, false, err
	}
	if count == 1 {
		if _, err = tx.Exec("INSERT INTO jobs(item_id) VALUES(?)", id); err != nil {
			return 0, false, err
		}
		if _, err = tx.Exec("INSERT INTO item_fts(rowid,title,text,url) VALUES(?,'','',?)", id, canonical); err != nil {
			return 0, false, err
		}
		if _, err = s.decideFilter(tx, id, nil, false); err != nil {
			return 0, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, count == 1, nil
}

var ErrLeaseLost = errors.New("job lease lost")

func (s *Store) Claim(ctx context.Context, now time.Time, lease time.Duration, maxAttempts int) (*Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// A crash on the final attempt must terminate instead of retrying forever.
	if _, err = tx.ExecContext(ctx, `UPDATE items SET state='needs_input',error='fetch attempts exhausted after restart' WHERE selected_snapshot IS NULL AND id IN (SELECT item_id FROM jobs WHERE stage='fetch' AND state='running' AND lease_until<=? AND attempts>=?)`, now.UnixMilli(), maxAttempts); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',error='fetch attempts exhausted after restart' WHERE stage='fetch' AND state='running' AND lease_until<=? AND attempts>=?`, now.UnixMilli(), maxAttempts); err != nil {
		return nil, err
	}
	if s.DigestConfig != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE items SET state='digest_failed',error='digest attempts exhausted after restart' WHERE NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE snapshot_id=items.selected_snapshot) AND selected_snapshot IN (SELECT snapshot_id FROM jobs WHERE stage='digest' AND state='running' AND lease_until<=? AND attempts>=?)`, now.UnixMilli(), maxAttempts); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='failed',error='digest attempts exhausted after restart' WHERE stage='digest' AND state='running' AND lease_until<=? AND attempts>=?`, now.UnixMilli(), maxAttempts); err != nil {
			return nil, err
		}
	}
	var j Job
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.item_id,i.canonical_url,j.attempts,i.selected_snapshot,j.stage,j.snapshot_id,j.config_version,j.model,j.prompt_version,j.input_limit,j.refresh,j.filter_override,j.interests,j.chunk_limit,j.chunk_max_parts FROM jobs j JOIN active_items i ON i.id=j.item_id WHERE ((j.state IN ('queued','retry_wait') AND j.next_attempt<=?) OR (j.state='running' AND j.lease_until<=?)) AND (j.stage='fetch' OR ?) ORDER BY j.id LIMIT 1`, now.UnixMilli(), now.UnixMilli(), s.DigestConfig != nil).Scan(&j.ID, &j.ItemID, &j.URL, &j.Attempts, &j.SelectedAtClaim, &j.Stage, &j.SnapshotID, &j.ConfigVersion, &j.Model, &j.PromptVersion, &j.InputLimit, &j.Refresh, &j.FilterOverride, &j.Interests, &j.ChunkLimit, &j.ChunkMaxParts)
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
	if j.Stage == "digest" {
		_, err = tx.ExecContext(ctx, "UPDATE items SET state='enriching',error='' WHERE id=? AND selected_snapshot=? AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE snapshot_id=?)", j.ItemID, j.SnapshotID, j.SnapshotID)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE items SET state='fetching',error='' WHERE id=? AND selected_snapshot IS NULL", j.ItemID)
	}
	if err != nil {
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
	_, err := tx.Exec("UPDATE item_fts SET title=?,text=?,url=(SELECT canonical_url FROM items WHERE id=?) WHERE rowid=?", title, text, id, id)
	return err
}
func (s *Store) insertSnapshot(tx *sql.Tx, id int64, jobID any, snap Snapshot) (int64, error) {
	sum := sha256.Sum256([]byte(snap.Text))
	path, err := s.Files.Put("texts", []byte(snap.Text))
	if err != nil {
		return 0, err
	}
	retrieved := snap.RetrievedAt
	if retrieved == "" {
		retrieved = nowUTC()
	}
	r, err := tx.Exec("INSERT INTO source_versions(item_id,job_id,text_path,title,hash,retrieved_at,origin,extractor,completeness,warning) VALUES(?,?,?,?,?,?,?,?,?,?)", id, jobID, path, snap.Title, fmt.Sprintf("%x", sum), retrieved, snap.Origin, snap.Extractor, snap.Completeness, snap.Warning)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func (s *Store) Complete(j Job, snap Snapshot) error {
	if j.Stage != "fetch" {
		return errors.New("not a fetch job")
	}
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
	sid, err := s.insertSnapshot(tx, j.ItemID, j.ID, snap)
	if err != nil {
		return err
	}
	// Manual input wins over an already-running fetch.
	var origin string
	var selected sql.NullInt64
	if err = tx.QueryRow("SELECT coalesce((SELECT origin FROM source_versions WHERE id=selected_snapshot),''),selected_snapshot FROM items WHERE id=?", j.ItemID).Scan(&origin, &selected); err != nil {
		return err
	}
	jobState, jobMessage := "done", ""
	contentErr := ValidateSourceContent(snap)
	// Keep a prior selected source when a fresh response contains only a notice.
	// The new snapshot and source failure still remain in immutable history.
	if (contentErr == nil || !selected.Valid) && (origin != "manual" || selected == j.SelectedAtClaim) {
		if _, err = tx.Exec("UPDATE items SET selected_snapshot=?,title=?,state='ready',error='' WHERE id=?", sid, snap.Title, j.ItemID); err != nil {
			return err
		}
		if err = indexSnapshot(tx, j.ItemID, snap.Title, snap.Text); err != nil {
			return err
		}
		if err = s.processSnapshot(tx, j.ItemID, sid, snap, j.FilterOverride); err != nil {
			return err
		}
	}
	if contentErr != nil {
		jobState, jobMessage = "failed", contentErr.Error()
		if err = s.recordFailure(tx, j, FailureFromError(contentErr)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE jobs SET state=?,lease_until=0,error=? WHERE id=?", jobState, jobMessage, j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Fail(j Job, message string, retry bool, maxAttempts int, now time.Time, backoff time.Duration) error {
	return s.fail(j, message, retry, maxAttempts, now, backoff, nil)
}
func (s *Store) fail(j Job, message string, retry bool, maxAttempts int, now time.Time, backoff time.Duration, failure *JobFailure) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseValid(tx, j); err != nil {
		return err
	}
	if failure != nil {
		if err = s.recordFailure(tx, j, *failure); err != nil {
			return err
		}
	}
	state, itemState := "failed", "needs_input"
	if retry && j.Attempts < maxAttempts {
		state, itemState = "retry_wait", "retry_wait"
	}
	if _, err = tx.Exec("UPDATE jobs SET state=?,error=?,next_attempt=?,lease_until=0 WHERE id=?", state, message, now.Add(backoff).UnixMilli(), j.ID); err != nil {
		return err
	}
	if j.Stage == "digest" {
		itemState = "digest_failed"
		if state == "retry_wait" {
			itemState = "digest_retry_wait"
		}
		_, err = tx.Exec("UPDATE items SET state=?,error=? WHERE id=? AND selected_snapshot=? AND NOT EXISTS(SELECT 1 FROM generated_artifacts WHERE snapshot_id=?)", itemState, message, j.ItemID, j.SnapshotID, j.SnapshotID)
	} else {
		_, err = tx.Exec("UPDATE items SET state=?,error=? WHERE id=? AND selected_snapshot IS NULL", itemState, message, j.ItemID)
	}
	if err != nil {
		return err
	}
	if j.Stage == "fetch" && state == "failed" && (message == "джерело повернуло HTTP 404" || message == "джерело повернуло HTTP 410") {
		u, parseErr := url.Parse(j.URL)
		if parseErr != nil {
			return parseErr
		}
		if _, youtube, _ := youtubeID(u); !youtube {
			if err = trashMissingPage(tx, j.ItemID, message); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *Store) Retry(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var filtered string
	if err = tx.QueryRow("SELECT filter_state FROM active_items WHERE id=?", id).Scan(&filtered); err != nil {
		return err
	}
	if filtered == "rejected" {
		return errors.New("спершу явно поверніть матеріал до обробки")
	}

	r, err := tx.Exec("INSERT INTO jobs(item_id,refresh) SELECT id,1 FROM items WHERE id=? AND NOT EXISTS(SELECT 1 FROM jobs WHERE item_id=? AND stage='fetch' AND state IN ('queued','running','retry_wait'))", id, id)
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
	if err = tx.QueryRow("SELECT title FROM active_items WHERE id=?", id).Scan(&title); err != nil {
		return err
	}
	sid, err := s.insertSnapshot(tx, id, nil, Snapshot{Text: text, Title: title, Origin: "manual", Extractor: "manual-v1", Completeness: "unknown", Warning: "Повнота ручного тексту невідома"})
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE items SET selected_snapshot=?,state='ready',error='' WHERE id=?", sid, id); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE jobs SET state='cancelled' WHERE item_id=? AND stage='fetch' AND state IN ('queued','retry_wait')", id); err != nil {
		return err
	}
	if err = indexSnapshot(tx, id, title, text); err != nil {
		return err
	}
	if err = s.processSnapshot(tx, id, sid, Snapshot{Text: text, Title: title}, false); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Rate(id, score int64, source *int64, assessed *time.Time, reason string) error {
	return s.RateShown(id, score, source, nil, assessed, reason)
}
func (s *Store) RateShown(id, score int64, source, digest *int64, assessed *time.Time, reason string) error {
	return s.rateShown(id, score, source, digest, assessed, reason, LegacyRatingScale)
}
func (s *Store) rateShown(id, score int64, source, digest *int64, assessed *time.Time, reason, scale string) error {
	if scale == UsefulnessScale && (score < 0 || score > 6) {
		return errors.New("оберіть один із семи рівнів особистої корисності")
	}
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
	var did any
	if digest != nil {
		did = *digest
	}
	return s.update("INSERT INTO rating_events(item_id,score,recorded_at,assessed_at,source_version_id,digest_id,reason,scale) SELECT id,?,?,?,?,?,?,? FROM active_items WHERE id=?", score, nowUTC(), at, sid, did, reason, scale, id)
}
func (s *Store) SetPlacement(id int64, value string) error {
	if value != "inbox" && value != "library" && value != "deferred" {
		return errors.New("невідоме розміщення")
	}
	return s.update("UPDATE items SET placement=? WHERE id=? AND id IN (SELECT id FROM active_items)", value, id)
}
func (s *Store) SetRead(id int64, value bool) error {
	return s.update("UPDATE items SET is_read=? WHERE id=? AND id IN (SELECT id FROM active_items)", value, id)
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
	if err = tx.QueryRow("SELECT count(*) FROM active_items WHERE id=?", id).Scan(&n); err != nil {
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

const confidenceExpr = `(SELECT p.probability FROM generated_artifacts a LEFT JOIN interest_predictions p ON p.digest_id=a.id WHERE a.snapshot_id=i.selected_snapshot ORDER BY a.id DESC LIMIT 1)`
const itemSelect = `SELECT i.id,i.original_url,i.canonical_url,i.title,i.added_at,i.placement,i.state,i.error,i.is_read,i.selected_snapshot,(SELECT score FROM rating_events WHERE item_id=i.id ORDER BY id DESC LIMIT 1),coalesce((SELECT scale FROM rating_events WHERE item_id=i.id ORDER BY id DESC LIMIT 1),''),(SELECT group_concat(tag,', ') FROM (SELECT tag FROM item_tags WHERE item_id=i.id ORDER BY tag)),i.filter_state,` + confidenceExpr + ` FROM active_items i`

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var i Item
	var tags sql.NullString
	err := row.Scan(&i.ID, &i.OriginalURL, &i.URL, &i.Title, &i.AddedAt, &i.Placement, &i.State, &i.Error, &i.Read, &i.Selected, &i.Score, &i.ScoreScale, &tags, &i.FilterState, &i.Confidence)
	i.Tags = tags.String
	return i, err
}
func (s *Store) Item(id int64) (Item, error) {
	return scanItem(s.DB.QueryRow(itemSelect+" WHERE i.id=?", id))
}
func (s *Store) Snapshots(id int64) ([]Snapshot, error) {
	rows, err := s.DB.Query(snapshotSelect+" WHERE v.item_id=? ORDER BY v.id DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Snapshot
	for rows.Next() {
		v, path, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		data, e := s.Files.Read(path, v.Hash)
		if e != nil {
			return nil, e
		}
		v.Text = string(data)
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Store) Ratings(id int64) ([]RatingEvent, error) {
	rows, err := s.DB.Query("SELECT id,item_id,score,recorded_at,assessed_at,source_version_id,digest_id,reason,scale FROM rating_events WHERE item_id=? ORDER BY id DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RatingEvent
	for rows.Next() {
		var v RatingEvent
		if err = rows.Scan(&v.ID, &v.ItemID, &v.Score, &v.RecordedAt, &v.AssessedAt, &v.SourceVersionID, &v.DigestID, &v.Reason, &v.Scale); err != nil {
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
		query += " AND (i.id IN (SELECT rowid FROM item_fts WHERE item_fts MATCH ?) OR EXISTS(SELECT 1 FROM article_knowledge k WHERE k.item_id=i.id AND k.snapshot_id=i.selected_snapshot AND k.id=(SELECT a.id FROM article_knowledge a JOIN knowledge_runs r ON r.id=a.run_id WHERE a.item_id=i.id AND a.snapshot_id=i.selected_snapshot ORDER BY r.updated_at DESC,a.id DESC LIMIT 1) AND instr(k.search_text,?)>0))"
		args = append(args, strings.Join(terms, " AND "), strings.ToLower(f.Query))
	}
	for _, pair := range []struct{ v, column string }{{f.Placement, "i.placement"}, {f.State, "i.state"}} {
		if pair.v != "" {
			query += " AND " + pair.column + "=?"
			args = append(args, pair.v)
		}
	}
	if f.Filter != "" {
		if f.Filter != "not_checked" && f.Filter != "passed" && f.Filter != "rejected" && f.Filter != "overridden" {
			return nil, errors.New("invalid filter decision")
		}
		query += " AND i.filter_state=?"
		args = append(args, f.Filter)
	}
	if f.Category != "" {
		query += " AND EXISTS(SELECT 1 FROM article_knowledge k WHERE k.item_id=i.id AND k.snapshot_id=i.selected_snapshot AND k.id=(SELECT a.id FROM article_knowledge a JOIN knowledge_runs r ON r.id=a.run_id WHERE a.item_id=i.id AND a.snapshot_id=i.selected_snapshot ORDER BY r.updated_at DESC,a.id DESC LIMIT 1) AND k.category=?)"
		args = append(args, f.Category)
	}
	var minimum, maximum *float64
	for _, entry := range []struct {
		raw, operator string
		target        **float64
	}{{f.ConfidenceMin, ">=", &minimum}, {f.ConfidenceMax, "<=", &maximum}} {
		if entry.raw == "" {
			continue
		}
		value, err := strconv.ParseFloat(entry.raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return nil, errors.New("Confidence має бути 0..100")
		}
		*entry.target = &value
		query += " AND " + confidenceExpr + " " + entry.operator + " ?"
		args = append(args, value/100)
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return nil, errors.New("мінімальний Confidence перевищує максимальний")
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
	scaleExpr := "(SELECT scale FROM rating_events WHERE item_id=i.id ORDER BY id DESC LIMIT 1)"
	usefulnessExpr := "CASE WHEN " + scaleExpr + "='" + UsefulnessScale + "' THEN " + scoreExpr + " END"
	if f.Usefulness == "unrated" {
		query += " AND " + scoreExpr + " IS NULL"
	} else if f.Usefulness == "legacy" {
		query += " AND " + scaleExpr + "='" + LegacyRatingScale + "'"
	} else if f.Usefulness != "" {
		n, err := strconv.ParseInt(f.Usefulness, 10, 64)
		if err != nil || n < 0 || n > 6 {
			return nil, errors.New("невідомий рівень корисності")
		}
		query += " AND " + usefulnessExpr + "=?"
		args = append(args, n)
	}
	if len(f.Tags) > 0 {
		query += " AND (EXISTS(SELECT 1 FROM item_tags WHERE item_id=i.id AND tag IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.Tags)), ",") + ")) OR EXISTS(SELECT 1 FROM article_knowledge k,json_each(k.tags_json) t WHERE k.item_id=i.id AND k.snapshot_id=i.selected_snapshot AND k.id=(SELECT a.id FROM article_knowledge a JOIN knowledge_runs r ON r.id=a.run_id WHERE a.item_id=i.id AND a.snapshot_id=i.selected_snapshot ORDER BY r.updated_at DESC,a.id DESC LIMIT 1) AND t.value IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.Tags)), ",") + ")))"
		for range 2 {
			for _, tag := range f.Tags {
				args = append(args, tag)
			}
		}
	}
	direction := "DESC"
	if f.Direction == "asc" {
		direction = "ASC"
	}
	switch f.Sort {
	case "confidence":
		query += " ORDER BY " + confidenceExpr + " IS NULL," + confidenceExpr + " " + direction + ",i.id ASC"
	case "usefulness":
		query += " ORDER BY " + usefulnessExpr + " IS NULL," + usefulnessExpr + " " + direction + ",i.id ASC"
	case "rating":
		query += " ORDER BY " + scoreExpr + " IS NULL," + scoreExpr + " " + direction + ",i.id ASC"
	case "relevance":
		if f.Query == "" {
			return nil, errors.New("релевантність потребує запиту")
		}
		query += " ORDER BY coalesce((SELECT rank FROM item_fts WHERE rowid=i.id AND item_fts MATCH ?),1) ASC,i.id ASC"
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

type JobStatus struct {
	FailureKind string

	SnapshotID              sql.NullInt64
	ID                      int64
	State                   string
	Stage                   string
	Attempts                int
	Error                   string
	NextAttempt, LeaseUntil int64
}

func (s *Store) Jobs(id int64) ([]JobStatus, error) {
	rows, err := s.DB.Query("SELECT id,state,attempts,error,next_attempt,lease_until,stage,snapshot_id,coalesce((SELECT kind FROM job_failures WHERE job_id=jobs.id ORDER BY id DESC LIMIT 1),'') FROM jobs WHERE item_id=? ORDER BY id DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []JobStatus
	for rows.Next() {
		var j JobStatus
		if err = rows.Scan(&j.ID, &j.State, &j.Attempts, &j.Error, &j.NextAttempt, &j.LeaseUntil, &j.Stage, &j.SnapshotID, &j.FailureKind); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}
