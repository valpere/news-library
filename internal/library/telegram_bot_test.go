package library

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type tgSent struct {
	ChatID    int64
	Text      string
	ParseMode string
	Keyboard  [][]TGButton
}

// fakeTelegram is an in-memory Telegram API; nothing here touches the network.
type fakeTelegram struct {
	mu          sync.Mutex
	batches     [][]TGUpdate
	sent        []tgSent
	answers     []string
	rejectMarkV bool
	done        chan struct{}
}

func (f *fakeTelegram) Updates(ctx context.Context, offset int64, timeout int) ([]TGUpdate, error) {
	f.mu.Lock()
	if len(f.batches) > 0 {
		b := f.batches[0]
		f.batches = f.batches[1:]
		f.mu.Unlock()
		return b, nil
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil, nil
	}
}
func (f *fakeTelegram) Send(ctx context.Context, chatID int64, text, parseMode string, keyboard [][]TGButton) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectMarkV && parseMode != "" {
		return ErrTelegramBadMarkup
	}
	f.sent = append(f.sent, tgSent{chatID, text, parseMode, keyboard})
	return nil
}
func (f *fakeTelegram) Answer(ctx context.Context, callbackID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, callbackID+":"+text)
	return nil
}
func (f *fakeTelegram) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		out = append(out, m.Text)
	}
	return out
}

const botChat = int64(4242)

func newBot(t *testing.T) (*TelegramBot, *fakeTelegram, *Store) {
	s := testStore(t)
	api := &fakeTelegram{}
	return &TelegramBot{Store: s, API: api, ChatID: botChat, Origin: "http://127.0.0.1:8790"}, api, s
}
func textMessage(chat int64, text string) TGUpdate {
	return TGUpdate{UpdateID: 1, Message: &TGMessage{Chat: TGChat{ID: chat}, Text: text}}
}
func pressed(chat int64, data string) TGUpdate {
	return TGUpdate{UpdateID: 2, Callback: &TGCallback{ID: "cb", Data: data, Message: &TGMessage{Chat: TGChat{ID: chat}}}}
}

func TestBotIgnoresOtherChats(t *testing.T) {
	b, api, s := newBot(t)
	ctx := context.Background()
	b.HandleUpdate(ctx, textMessage(7, "https://a.example/1"))
	id := add(t, s, "https://a.example/x")
	b.HandleUpdate(ctx, pressed(7, "j:"+itoa(id)))
	if len(api.sent) != 0 || len(api.answers) != 0 || scalar[int](t, s, `SELECT count(*) FROM items`) != 1 || scalar[int](t, s, `SELECT count(*) FROM trash_entries`) != 0 {
		t.Fatalf("a foreign chat must change nothing: %+v %v", api.sent, api.answers)
	}
}

func TestBotAddsEveryLinkAndAnswersAtOnce(t *testing.T) {
	b, api, s := newBot(t)
	ctx := context.Background()
	b.HandleUpdate(ctx, textMessage(botChat, "глянь https://a.example/1 і (https://a.example/2), ще раз https://a.example/1"))
	if scalar[int](t, s, `SELECT count(*) FROM items WHERE telegram='pending'`) != 2 {
		t.Fatal("both links are added once")
	}
	got := api.texts()
	if len(got) != 1 || !strings.Contains(got[0], "Додано №1") || !strings.Contains(got[0], "Додано №2") || strings.Count(got[0], "№") != 2 {
		t.Fatalf("one reply with one line per distinct link: %q", got)
	}
	b.HandleUpdate(ctx, textMessage(botChat, "https://a.example/1"))
	if last := api.texts()[1]; !strings.Contains(last, "Вже є №1") {
		t.Fatalf("%q", last)
	}
	if err := s.MarkJunk(2, ""); err != nil {
		t.Fatal(err)
	}
	b.HandleUpdate(ctx, textMessage(botChat, "https://a.example/2"))
	if last := api.texts()[2]; !strings.Contains(last, "У смітнику №2") {
		t.Fatalf("%q", last)
	}
	b.HandleUpdate(ctx, textMessage(botChat, "привіт"))
	if last := api.texts()[3]; !strings.Contains(last, "посилання") {
		t.Fatalf("a message without links gets a hint: %q", last)
	}
}

func TestBotDeliversDigestOnceWithButtons(t *testing.T) {
	b, api, s := newBot(t)
	ctx := context.Background()
	id, _, _ := s.AddFromTelegram("https://a.example/1")
	if err := b.DeliverPending(ctx); err != nil || len(api.sent) != 0 {
		t.Fatalf("nothing is sent before processing finishes: %v %v", err, api.sent)
	}
	s.DB.Exec(`UPDATE items SET state='ready',title='Заголовок (тест)' WHERE id=?`, id)
	s.DB.Exec(`INSERT INTO texts(item_id,title,hash,retrieved_at,origin,extractor,completeness,warning,text_path) VALUES(?,?,?,?,?,?,?,?,?)`, id, "t", "h", "2026-01-01T00:00:00Z", "fetch", "x", "complete", "", "texts/x")
	path, err := s.Files.Put("digests", []byte("## Про що матеріал\nКоротко **головне**."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO digests(item_id,model,actual_model,provider,prompt_version,markdown_path,hash) VALUES(?,?,?,?,?,?,?)`, id, "m", "m", "p", "v", path, textHash("## Про що матеріал\nКоротко **головне**.")); err != nil {
		t.Fatal(err)
	}
	if err = b.DeliverPending(ctx); err != nil || len(api.sent) != 1 {
		t.Fatalf("%v %d", err, len(api.sent))
	}
	m := api.sent[0]
	if m.ChatID != botChat || m.ParseMode != "MarkdownV2" || !strings.Contains(m.Text, "*Про що матеріал*") || !strings.Contains(m.Text, "*головне*") || !strings.Contains(m.Text, "*Заголовок \\(тест\\)*") || !strings.Contains(m.Text, "127\\.0\\.0\\.1:8790/items/"+itoa(id)) {
		t.Fatalf("%+v", m)
	}
	rate := m.Keyboard[0]
	if len(rate) != 7 || rate[0].Data != "r:"+itoa(id)+":0" || rate[6].Data != "r:"+itoa(id)+":6" {
		t.Fatalf("rating row %+v", rate)
	}
	if last := m.Keyboard[len(m.Keyboard)-1]; last[0].Data != "j:"+itoa(id) {
		t.Fatalf("junk button %+v", m.Keyboard)
	}
	for _, row := range m.Keyboard {
		for _, button := range row {
			if button.URL != "" {
				t.Fatalf("a loopback card address must not be a URL button: %+v", button)
			}
		}
	}
	if err = b.DeliverPending(ctx); err != nil || len(api.sent) != 1 {
		t.Fatal("a delivered material is never sent again")
	}
}

func TestBotUsesURLButtonForPublicOriginAndFallsBackToPlainText(t *testing.T) {
	b, api, s := newBot(t)
	b.Origin = "https://library.example.org"
	api.rejectMarkV = true
	id, _, _ := s.AddFromTelegram("https://a.example/1")
	s.DB.Exec(`UPDATE items SET state='needs_input',error='субтитри недоступні' WHERE id=?`, id)
	if err := b.DeliverPending(context.Background()); err != nil || len(api.sent) != 1 {
		t.Fatalf("%v %d", err, len(api.sent))
	}
	m := api.sent[0]
	if m.ParseMode != "" || !strings.Contains(m.Text, "субтитри недоступні") || !strings.Contains(m.Text, "№"+itoa(id)) {
		t.Fatalf("a failed material is reported in plain text after markup rejection: %+v", m)
	}
	var card string
	for _, row := range m.Keyboard {
		for _, button := range row {
			card += button.URL
		}
	}
	if card != "https://library.example.org/items/"+itoa(id) {
		t.Fatalf("card button %q", card)
	}
}

func TestBotButtonsRateAndTrash(t *testing.T) {
	b, api, s := newBot(t)
	ctx := context.Background()
	id := add(t, s, "https://a.example/1")
	b.HandleUpdate(ctx, pressed(botChat, "r:"+itoa(id)+":5"))
	if scalar[int](t, s, `SELECT rating FROM items WHERE id=?`, id) != 5 {
		t.Fatal("rating")
	}
	b.HandleUpdate(ctx, pressed(botChat, "r:"+itoa(id)+":9"))
	b.HandleUpdate(ctx, pressed(botChat, "bogus"))
	if scalar[int](t, s, `SELECT rating FROM items WHERE id=?`, id) != 5 {
		t.Fatal("an invalid score changes nothing")
	}
	b.HandleUpdate(ctx, pressed(botChat, "j:"+itoa(id)))
	if scalar[int](t, s, `SELECT count(*) FROM trash_entries WHERE item_id=?`, id) != 1 {
		t.Fatal("junk")
	}
	if len(api.answers) != 4 || !strings.HasPrefix(api.answers[0], "cb:") {
		t.Fatalf("every press is answered so the button stops spinning: %v", api.answers)
	}
}

func TestBotRunHandlesUpdatesAndDeliversUntilStopped(t *testing.T) {
	b, api, s := newBot(t)
	b.PollSeconds, b.DeliverEvery, b.ErrorPause = 0, 5*time.Millisecond, time.Millisecond
	api.batches = [][]TGUpdate{{textMessage(botChat, "https://a.example/1")}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(api.texts()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if scalar[int](t, s, `SELECT count(*) FROM items`) != 1 {
		t.Fatal("link from the polled update was added")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must stop with its context")
	}
}

func TestBotKeepsRunningAfterAPIErrors(t *testing.T) {
	b, _, _ := newBot(t)
	b.API = errorsTelegram{}
	b.PollSeconds, b.DeliverEvery, b.ErrorPause = 0, 5*time.Millisecond, time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	b.Run(ctx)
}

type errorsTelegram struct{}

func (errorsTelegram) Updates(context.Context, int64, int) ([]TGUpdate, error) {
	return nil, errors.New("network down")
}
func (errorsTelegram) Send(context.Context, int64, string, string, [][]TGButton) error {
	return errors.New("network down")
}
func (errorsTelegram) Answer(context.Context, string, string) error { return nil }

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
