package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var telegramURL = regexp.MustCompile(`https?://[^\s<>"]+`)

const telegramMessageLimit = 4096

// TelegramBot accepts links from one chat, adds them as materials and sends each
// one's digest once, with buttons to rate it or move it to the trash. Messages
// from other chats are ignored.
type TelegramBot struct {
	Store  *Store
	API    TelegramAPI
	ChatID int64
	// Origin is the library's address, used for the card link.
	Origin       string
	PollSeconds  int
	DeliverEvery time.Duration
	ErrorPause   time.Duration

	failures map[int64]int
}

// maxDeliveryFailures is how many times a material is tried before the bot leaves
// it pending until the next restart (it is never marked sent unsent).
const maxDeliveryFailures = 5

// Run polls for updates and delivers finished materials until ctx ends.
func (b *TelegramBot) Run(ctx context.Context) {
	poll, deliver, pause := b.PollSeconds, b.DeliverEvery, b.ErrorPause
	if deliver <= 0 {
		deliver = 10 * time.Second
	}
	if pause <= 0 {
		pause = 5 * time.Second
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if err := b.DeliverPending(ctx); err != nil && ctx.Err() == nil {
				log.Printf("telegram delivery: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(deliver):
			}
		}
	}()
	var offset int64
	for ctx.Err() == nil {
		updates, err := b.API.Updates(ctx, offset, poll)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("telegram polling: %v", err)
				select {
				case <-ctx.Done():
				case <-time.After(pause):
				}
			}
			continue
		}
		for _, u := range updates {
			offset = max(offset, u.UpdateID+1)
			b.HandleUpdate(ctx, u)
		}
	}
	wg.Wait()
}

// HandleUpdate processes one incoming message or button press.
func (b *TelegramBot) HandleUpdate(ctx context.Context, u TGUpdate) {
	switch {
	case u.Message != nil && u.Message.Chat.ID == b.ChatID:
		b.handleMessage(ctx, u.Message.Text)
	case u.Callback != nil && u.Callback.Message != nil && u.Callback.Message.Chat.ID == b.ChatID:
		b.handleButton(ctx, u.Callback)
	}
}

func (b *TelegramBot) say(ctx context.Context, text string) {
	if err := b.API.Send(ctx, b.ChatID, text, "", nil); err != nil && ctx.Err() == nil {
		log.Printf("telegram reply: %v", err)
	}
}

func (b *TelegramBot) handleMessage(ctx context.Context, text string) {
	seen := map[string]bool{}
	var lines []string
	for _, raw := range telegramURL.FindAllString(text, -1) {
		raw = trimLinkEnd(raw)
		if seen[raw] {
			continue
		}
		seen[raw] = true
		id, status, err := b.Store.AddFromTelegram(raw)
		switch {
		case err != nil:
			lines = append(lines, "Не вдалося додати: "+raw)
		case status == TelegramAdded:
			lines = append(lines, fmt.Sprintf("Додано №%d: %s", id, raw))
		case status == TelegramTrashed:
			lines = append(lines, fmt.Sprintf("У смітнику №%d: %s", id, raw))
		default:
			lines = append(lines, fmt.Sprintf("Вже є №%d: %s", id, raw))
		}
	}
	if len(lines) == 0 {
		lines = []string{"Надішліть посилання на статтю або відео."}
	}
	b.say(ctx, strings.Join(lines, "\n"))
}

func (b *TelegramBot) handleButton(ctx context.Context, c *TGCallback) {
	answer := "Не зрозумів кнопку"
	parts := strings.Split(c.Data, ":")
	var err error
	switch {
	case len(parts) == 3 && parts[0] == "r":
		id, e1 := strconv.ParseInt(parts[1], 10, 64)
		score, e2 := strconv.ParseInt(parts[2], 10, 64)
		if e1 == nil && e2 == nil && score >= 0 && score <= 6 {
			now := time.Now()
			if err = b.Store.Rate(id, score, &now); err == nil {
				answer = fmt.Sprintf("Оцінено №%d: %d", id, score)
			} else {
				answer = "Не вдалося оцінити"
			}
		}
	case len(parts) == 2 && parts[0] == "j":
		if id, e := strconv.ParseInt(parts[1], 10, 64); e == nil {
			if err = b.Store.MarkJunk(id, "Telegram"); err == nil {
				answer = fmt.Sprintf("№%d у смітнику", id)
			} else {
				answer = "Не вдалося перенести в смітник"
			}
		}
	}
	if err != nil {
		log.Printf("telegram button: %v", err)
	}
	if e := b.API.Answer(ctx, c.ID, answer); e != nil && ctx.Err() == nil {
		log.Printf("telegram answer: %v", e)
	}
}

// DeliverPending sends every finished, not yet sent material once.
func (b *TelegramBot) DeliverPending(ctx context.Context) error {
	ids, err := b.Store.TelegramPending()
	if err != nil {
		return err
	}
	if b.failures == nil {
		b.failures = map[int64]int{}
	}
	var failed []error
	for _, id := range ids {
		if b.failures[id] >= maxDeliveryFailures {
			continue
		}
		if err = b.deliver(ctx, id); err == nil {
			err = b.Store.MarkTelegramSent(id)
		}
		if err != nil {
			b.failures[id]++
			failed = append(failed, fmt.Errorf("material %d: %w", id, err))
		}
	}
	return errors.Join(failed...)
}

// trimLinkEnd removes sentence punctuation after a link and closing brackets that
// have no opening one inside it, so "(https://a.example/x)" and
// ".../Go_(language)." both keep the right address.
func trimLinkEnd(raw string) string {
	for raw != "" {
		last := raw[len(raw)-1]
		switch {
		case strings.IndexByte(".,;:!?\"'", last) >= 0:
		case last == ')' && strings.Count(raw, ")") > strings.Count(raw, "("):
		case last == ']' && strings.Count(raw, "]") > strings.Count(raw, "["):
		case last == '}' && strings.Count(raw, "}") > strings.Count(raw, "{"):
		default:
			return raw
		}
		raw = raw[:len(raw)-1]
	}
	return raw
}

func clip(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}
	return string([]rune(s)[:runes]) + "…"
}

// send delivers a message; a button URL Telegram refuses is dropped, because the
// card address is in the text anyway.
func (b *TelegramBot) send(ctx context.Context, text, parseMode string, keyboard [][]TGButton) error {
	err := b.API.Send(ctx, b.ChatID, text, parseMode, keyboard)
	if errors.Is(err, ErrTelegramBadButton) {
		var kept [][]TGButton
		for _, row := range keyboard {
			var r []TGButton
			for _, button := range row {
				if button.URL == "" {
					r = append(r, button)
				}
			}
			if len(r) > 0 {
				kept = append(kept, r)
			}
		}
		err = b.API.Send(ctx, b.ChatID, text, parseMode, kept)
	}
	return err
}

func (b *TelegramBot) cardURL(id int64) string {
	return b.Origin + "/items/" + strconv.FormatInt(id, 10)
}

func (b *TelegramBot) publicOrigin() bool {
	u, err := url.Parse(b.Origin)
	if err != nil || u.Hostname() == "" || u.Hostname() == "localhost" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate()
	}
	host := strings.ToLower(u.Hostname())
	for _, local := range []string{".local", ".lan", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, local) {
			return false
		}
	}
	return strings.Contains(host, ".")
}

func (b *TelegramBot) keyboard(id int64, withRating bool) [][]TGButton {
	var rows [][]TGButton
	n := strconv.FormatInt(id, 10)
	if withRating {
		var rate []TGButton
		for score := 0; score <= 6; score++ {
			rate = append(rate, TGButton{Text: strconv.Itoa(score), Data: "r:" + n + ":" + strconv.Itoa(score)})
		}
		rows = append(rows, rate)
	}
	last := []TGButton{{Text: "Сміття", Data: "j:" + n}}
	if b.publicOrigin() {
		last = append(last, TGButton{Text: "Картка", URL: b.cardURL(id)})
	}
	return append(rows, last)
}

func (b *TelegramBot) deliver(ctx context.Context, id int64) error {
	item, err := b.Store.Item(id)
	if err != nil {
		return err
	}
	title := item.Title
	if title == "" {
		title = item.URL
	}
	title = clip(title, 200)
	digest, err := b.Store.DigestOf(id)
	if err != nil {
		return err
	}
	var markdown, plain string
	rated := item.State == "ready"
	switch {
	case digest != nil:
		body, cut := TelegramFit(digest.Markdown, telegramMessageLimit-len(title)*2-len(b.cardURL(id))*2-40)
		markdown = "*" + tgEscape(title) + "*\n\n" + TelegramMarkdown(body)
		plain = title + "\n\n" + body
		if cut {
			markdown += "\n\n…"
			plain += "\n\n…"
		}
	case item.State == "ready":
		plain = fmt.Sprintf("№%d готово без дайджесту (короткий матеріал): %s", id, title)
	case item.State == "filtered":
		plain = fmt.Sprintf("№%d відсіяно правилами: %s", id, title)
	default:
		reason := clip(item.Error, 500)
		if reason == "" {
			reason = "потрібен ручний текст або повторна спроба"
		}
		plain = fmt.Sprintf("Не вдалося обробити №%d: %s\n%s", id, title, reason)
	}
	card := "\n\n" + b.cardURL(id)
	keyboard := b.keyboard(id, rated)
	if markdown != "" {
		err = b.send(ctx, markdown+"\n\n"+tgEscape(b.cardURL(id)), "MarkdownV2", keyboard)
		if err == nil || !errors.Is(err, ErrTelegramBadMarkup) {
			return err
		}
	}
	return b.send(ctx, plain+card, "", keyboard)
}
