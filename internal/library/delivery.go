package library

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type TelegramConfig struct {
	Enabled                  bool
	Token, ChatID, DailyTime string
	Size                     int
}

var botTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,128}$`)

func (c TelegramConfig) Validate() error {
	if c.Size < 1 || c.Size > 20 {
		return errors.New("telegram size must be 1..20")
	}
	if c.DailyTime != "" {
		if _, err := time.Parse("15:04", c.DailyTime); err != nil {
			return errors.New("telegram daily time must be HH:MM")
		}
	}
	if c.Enabled {
		id, err := strconv.ParseInt(c.ChatID, 10, 64)
		if !botTokenPattern.MatchString(c.Token) || err != nil || id == 0 {
			return errors.New("configure private Telegram bot token and numeric chat ID")
		}
	}
	return nil
}
func (c TelegramConfig) BotID() string {
	if !botTokenPattern.MatchString(c.Token) {
		return ""
	}
	id, _, _ := strings.Cut(c.Token, ":")
	return id
}

type Delivery struct {
	ID                                              int64
	ChatID, BotID, Trigger, State, CreatedAt, Error string
	Parts                                           []DeliveryPart
}
type DeliveryPart struct {
	DeliveryID, ItemID, SnapshotID, DigestID int64
	Ordinal                                  int
	Text, State, Token                       string
	MessageID                                sql.NullInt64
}

func telegramLimit(text string, limit int) string {
	total := 0
	for _, r := range text {
		total++
		if r > 0xffff {
			total++
		}
	}
	if total <= limit {
		return text
	}
	units := 0
	for i, r := range text {
		cost := 1
		if r > 0xffff {
			cost = 2
		}
		if units+cost > limit-16 {
			return text[:i] + "\n[Скорочено]"
		}
		units += cost
	}
	return text
}

var ErrEmptyDelivery = errors.New("немає готових нових дайджестів для добірки")

func deliveryText(item Item, snapshot Snapshot, digest Digest) string {
	rating := "не оцінено"
	if item.Score.Valid {
		rating = strconv.FormatInt(item.Score.Int64, 10)
	}
	header := telegramLimit(item.Title, 160)
	if header == "" {
		header = "Матеріал #" + strconv.FormatInt(item.ID, 10)
	}
	return header + "\n" + telegramLimit(item.URL, 500) + "\n\n" + telegramLimit(digest.Markdown, 2600) + "\n\nВласна оцінка: " + rating + " · " + map[string]string{"inbox": "вхідні", "library": "у бібліотеці", "deferred": "відкладено"}[item.Placement] + "\nТекст #" + strconv.FormatInt(snapshot.ID, 10) + " · дайджест #" + strconv.FormatInt(digest.ID, 10) + completenessSuffix(snapshot)
}
func (s *Store) PreviewDelivery(c TelegramConfig, trigger, slot string) (int64, error) {
	if err := c.Validate(); err != nil {
		return 0, err
	}
	if trigger != "manual" && trigger != "schedule" {
		return 0, errors.New("invalid delivery trigger")
	}
	// Freeze selection before send; no generation or inferred personal filter.
	items, err := s.Search(Filters{Read: "0", Sort: "added"})
	if err != nil {
		return 0, err
	}
	type part struct {
		item       Item
		snapshot   Snapshot
		digest     Digest
		path, hash string
	}
	var parts []part
	for _, item := range items {
		if item.FilterState == "rejected" || !item.Selected.Valid || (item.Score.Valid && item.Score.Int64 == 0) {
			continue
		}
		digest, e := s.DigestForSnapshot(item.Selected.Int64)
		if e != nil {
			return 0, e
		}
		if digest == nil {
			continue
		}
		var sent int
		if e = s.DB.QueryRow(`SELECT count(*) FROM delivery_parts p JOIN deliveries d ON d.id=p.delivery_id WHERE p.digest_id=? AND (p.state IN ('sent','sending','unknown') OR (p.state='queued' AND d.state IN ('queued','sending'))) AND d.chat_id=?`, digest.ID, c.ChatID).Scan(&sent); e != nil {
			return 0, e
		}
		if sent != 0 {
			continue
		}
		snapshot, e := s.Snapshot(item.Selected.Int64)
		if e != nil {
			return 0, e
		}
		text := deliveryText(item, snapshot, *digest)
		path, e := s.Files.Put("digests", []byte(text))
		if e != nil {
			return 0, e
		}
		parts = append(parts, part{item, snapshot, *digest, path, textHash(text)})
		if len(parts) == c.Size {
			break
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var slotValue any
	if slot != "" {
		slotValue = slot
	}
	state := "preview"
	if trigger == "schedule" {
		state = "queued"
	}
	if len(parts) == 0 {
		return 0, ErrEmptyDelivery
	}
	result, err := tx.Exec(`INSERT INTO deliveries(chat_id,bot_id,trigger,slot,state,created_at) VALUES(?,?,?,?,?,?)`, c.ChatID, c.BotID(), trigger, slotValue, state, nowUTC())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for ordinal, p := range parts {
		if trigger == "schedule" {
			var duplicate int
			if err = tx.QueryRow(`SELECT count(*) FROM delivery_parts p JOIN deliveries d ON d.id=p.delivery_id WHERE p.digest_id=? AND d.chat_id=? AND (p.state IN ('sent','sending','unknown') OR d.state IN ('queued','sending'))`, p.digest.ID, c.ChatID).Scan(&duplicate); err != nil {
				return 0, err
			}
			if duplicate > 0 {
				return 0, ErrEmptyDelivery
			}
		}

		_, err = tx.Exec(`INSERT INTO delivery_parts(delivery_id,ordinal,item_id,snapshot_id,digest_id,text_path,hash,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, ordinal, p.item.ID, p.snapshot.ID, p.digest.ID, p.path, p.hash, nowUTC())
		if err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}
func (s *Store) Deliveries() ([]Delivery, error) {
	rows, err := s.DB.Query(`SELECT id,chat_id,bot_id,trigger,state,created_at,error FROM deliveries ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Delivery
	for rows.Next() {
		var d Delivery
		if err = rows.Scan(&d.ID, &d.ChatID, &d.BotID, &d.Trigger, &d.State, &d.CreatedAt, &d.Error); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Store) Delivery(id int64) (Delivery, error) {
	var d Delivery
	err := s.DB.QueryRow(`SELECT id,chat_id,bot_id,trigger,state,created_at,error FROM deliveries WHERE id=?`, id).Scan(&d.ID, &d.ChatID, &d.BotID, &d.Trigger, &d.State, &d.CreatedAt, &d.Error)
	if err != nil {
		return d, err
	}
	rows, err := s.DB.Query(`SELECT ordinal,item_id,snapshot_id,digest_id,text_path,hash,state,message_id FROM delivery_parts WHERE delivery_id=? ORDER BY ordinal`, id)
	if err != nil {
		return d, err
	}
	type saved struct {
		part       DeliveryPart
		path, hash string
	}
	var records []saved
	for rows.Next() {
		v := saved{}
		v.part.DeliveryID = id
		if err = rows.Scan(&v.part.Ordinal, &v.part.ItemID, &v.part.SnapshotID, &v.part.DigestID, &v.path, &v.hash, &v.part.State, &v.part.MessageID); err != nil {
			break
		}
		records = append(records, v)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if rowErr != nil {
		return d, rowErr
	}
	for _, v := range records {
		data, e := s.Files.Read(v.path, v.hash)
		if e != nil {
			return d, e
		}
		v.part.Text = string(data)
		d.Parts = append(d.Parts, v.part)
	}
	return d, nil
}
func (s *Store) QueueDelivery(id int64, c TelegramConfig, retry bool) error {
	if !c.Enabled {
		return errors.New("Telegram delivery is disabled")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state, chat, bot string
	if err = tx.QueryRow(`SELECT state,chat_id,bot_id FROM deliveries WHERE id=?`, id).Scan(&state, &chat, &bot); err != nil {
		return err
	}
	if chat != c.ChatID || bot != c.BotID() {
		return errors.New("recipient or bot changed; prepare a new preview")
	}
	var duplicate int
	if err = tx.QueryRow(`SELECT count(*) FROM delivery_parts p JOIN deliveries d ON d.id=p.delivery_id WHERE d.id<>? AND d.chat_id=? AND (p.state IN ('sent','sending','unknown') OR d.state IN ('queued','sending')) AND p.digest_id IN (SELECT digest_id FROM delivery_parts WHERE delivery_id=? AND state<>'sent')`, id, c.ChatID, id).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return errors.New("these digests already have an active or uncertain delivery")
	}
	if retry {
		if state != "failed" {
			return errors.New("only definitely failed delivery can be retried")
		}
		if _, err = tx.Exec(`UPDATE delivery_parts SET state='queued',token='',updated_at=? WHERE delivery_id=? AND state='failed'`, nowUTC(), id); err != nil {
			return err
		}
	} else if state != "preview" {
		if state == "queued" || state == "sending" || state == "sent" {
			return nil
		}
		return errors.New("delivery cannot be queued")
	}
	_, err = tx.Exec(`UPDATE deliveries SET state='queued',error='' WHERE id=?`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RecoverDeliveries() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE deliveries SET state='unknown',error='Попередню відправку перервано; перевірте Telegram. Автоматичного повтору немає.' WHERE state='sending'`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE delivery_parts SET state='unknown',updated_at=? WHERE state='sending'`, nowUTC()); err != nil {
		return err
	}
	return tx.Commit()
}

type SendError struct{ Unknown bool }

func (e *SendError) Error() string {
	if e.Unknown {
		return "Результат відправки невідомий; перевірте Telegram. Автоматичного повтору немає."
	}
	return "Telegram відхилив відправку; можна повторити вручну."
}

type MessageSender interface {
	Send(context.Context, string, string) (int64, error)
}
type Telegram struct {
	token, base string
	client      *http.Client
}

func NewTelegram(token string, timeout time.Duration) (*Telegram, error) {
	if !botTokenPattern.MatchString(token) {
		return nil, errors.New("invalid Telegram token")
	}
	return &Telegram{token: token, base: "https://api.telegram.org", client: &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect disabled") }}}, nil
}
func (t *Telegram) Send(ctx context.Context, chat, text string) (int64, error) {
	payload, _ := json.Marshal(map[string]any{"chat_id": chat, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}})
	req, err := http.NewRequestWithContext(ctx, "POST", t.base+"/bot"+t.token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return 0, &SendError{Unknown: true}
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(req)
	if err != nil {
		return 0, &SendError{Unknown: true}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, &SendError{Unknown: true}
	}
	var result struct {
		OK        *bool `json:"ok"`
		ErrorCode int   `json:"error_code"`
		Result    struct {
			ID int64 `json:"message_id"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &result) != nil || result.OK == nil {
		return 0, &SendError{Unknown: true}
	}
	if !*result.OK && result.ErrorCode >= 400 {
		return 0, &SendError{}
	}
	if response.StatusCode == 200 && *result.OK && result.Result.ID > 0 {
		return result.Result.ID, nil
	}
	return 0, &SendError{Unknown: true}
}

type DeliveryWorker struct {
	Store   *Store
	Sender  MessageSender
	Config  TelegramConfig
	Timeout time.Duration
}

func (w *DeliveryWorker) Step(ctx context.Context) (bool, error) {
	if !w.Config.Enabled || w.Sender == nil {
		return false, nil
	}
	if err := w.Config.Validate(); err != nil {
		return false, err
	}
	tx, err := w.Store.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Never redirect old queued previews to a newly configured recipient/bot.
	_, err = tx.Exec(`UPDATE deliveries SET state='failed',error='Адресат або бот змінився; створіть нову добірку.' WHERE state='queued' AND (chat_id<>? OR bot_id<>?)`, w.Config.ChatID, w.Config.BotID())
	if err != nil {
		return false, err
	}
	var p DeliveryPart
	var path, hash string
	err = tx.QueryRow(`SELECT p.delivery_id,p.ordinal,p.text_path,p.hash FROM delivery_parts p JOIN deliveries d ON d.id=p.delivery_id WHERE d.state='queued' AND p.state='queued' AND d.chat_id=? AND d.bot_id=? ORDER BY d.id,p.ordinal LIMIT 1`, w.Config.ChatID, w.Config.BotID()).Scan(&p.DeliveryID, &p.Ordinal, &path, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	p.Token = rand.Text()
	if _, err = tx.Exec(`UPDATE delivery_parts SET state='sending',token=?,updated_at=? WHERE delivery_id=? AND ordinal=?`, p.Token, nowUTC(), p.DeliveryID, p.Ordinal); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE deliveries SET state='sending' WHERE id=?`, p.DeliveryID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	data, err := w.Store.Files.Read(path, hash)
	var message int64
	if err != nil {
		err = &SendError{}
	}
	if err == nil {
		work, cancel := context.WithTimeout(ctx, w.Timeout)
		message, err = w.Sender.Send(work, w.Config.ChatID, string(data))
		cancel()
	}
	if err == nil && message <= 0 {
		err = &SendError{Unknown: true}
	}
	state, reason := "sent", ""
	if err != nil {
		state = "unknown"
		var send *SendError
		if errors.As(err, &send) && !send.Unknown {
			state = "failed"
		}
		reason = (&SendError{Unknown: state == "unknown"}).Error()
	}
	tx, saveErr := w.Store.DB.Begin()
	if saveErr != nil {
		return true, saveErr
	}
	defer tx.Rollback()
	var messageID any
	if state == "sent" {
		messageID = message
	}
	result, saveErr := tx.Exec(`UPDATE delivery_parts SET state=?,message_id=?,updated_at=? WHERE delivery_id=? AND ordinal=? AND token=? AND state='sending'`, state, messageID, nowUTC(), p.DeliveryID, p.Ordinal, p.Token)
	if saveErr != nil {
		return true, saveErr
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return true, ErrLeaseLost
	}
	aggregate := state
	if state == "sent" {
		var remaining int
		if saveErr = tx.QueryRow(`SELECT count(*) FROM delivery_parts WHERE delivery_id=? AND state<>'sent'`, p.DeliveryID).Scan(&remaining); saveErr != nil {
			return true, saveErr
		}
		if remaining > 0 {
			aggregate = "queued"
		}
	}
	if _, saveErr = tx.Exec(`UPDATE deliveries SET state=?,error=? WHERE id=? AND state='sending'`, aggregate, reason, p.DeliveryID); saveErr != nil {
		return true, saveErr
	}
	return true, tx.Commit()
}
func (w *DeliveryWorker) Schedule(now time.Time) error {
	if !w.Config.Enabled || w.Config.DailyTime == "" {
		return nil
	}
	zone, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return err
	}
	local := now.In(zone)
	slot := "daily:" + local.Format("2006-01-02")
	due, err := time.ParseInLocation("2006-01-02 15:04", local.Format("2006-01-02")+" "+w.Config.DailyTime, zone)
	if err != nil {
		return err
	}
	if now.Before(due) {
		return nil
	}
	var exists int
	if err = w.Store.DB.QueryRow(`SELECT count(*) FROM deliveries WHERE slot=?`, slot).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	if now.Sub(due) >= 5*time.Minute {
		_, err = w.Store.DB.Exec(`INSERT INTO deliveries(chat_id,bot_id,trigger,slot,state,created_at,error) VALUES(?,?,'schedule',?,'missed',?,'Випуск пропущено; автоматичного надолуження немає.') ON CONFLICT(slot) DO NOTHING`, w.Config.ChatID, w.Config.BotID(), slot, nowUTC())
		return err
	}
	_, err = w.Store.PreviewDelivery(w.Config, "schedule", slot)
	if err != nil {
		// Empty selection is an observed skipped slot, not repeated generation.
		if errors.Is(err, ErrEmptyDelivery) {
			_, err = w.Store.DB.Exec(`INSERT INTO deliveries(chat_id,bot_id,trigger,slot,state,created_at,error) VALUES(?,?,'schedule',?,'missed',?,'Немає нових готових дайджестів.') ON CONFLICT(slot) DO NOTHING`, w.Config.ChatID, w.Config.BotID(), slot, nowUTC())
		}
	}
	return err
}
func (w *DeliveryWorker) Run(ctx context.Context) {
	recoverNeeded := false
	for ctx.Err() == nil {
		if recoverNeeded {
			if w.Store.RecoverDeliveries() == nil {
				recoverNeeded = false
			}
		}
		_ = w.Schedule(time.Now())
		worked, err := w.Step(ctx)
		if err != nil {
			recoverNeeded = true
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func completenessSuffix(snapshot Snapshot) string {
	if label := Assess(snapshot).Label(); label != "" {
		return " · " + label
	}
	return ""
}
