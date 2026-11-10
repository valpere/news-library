package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "123456:SECRET-token"

func TestTelegramClientSpeaksTheBotAPI(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.URL.Path+" "+string(body))
		switch r.URL.Path {
		case "/bot" + testToken + "/getUpdates":
			io.WriteString(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"chat":{"id":42},"text":"https://a.example/1"}},{"update_id":8,"callback_query":{"id":"c1","data":"j:3","message":{"message_id":2,"chat":{"id":42}}}}]}`)
		case "/bot" + testToken + "/sendMessage":
			io.WriteString(w, `{"ok":true,"result":{"message_id":9}}`)
		case "/bot" + testToken + "/answerCallbackQuery":
			io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := &TelegramClient{Token: testToken, BaseURL: server.URL, HTTP: server.Client()}
	ctx := context.Background()
	updates, err := c.Updates(ctx, 7, 25)
	if err != nil || len(updates) != 2 || updates[0].UpdateID != 7 || updates[0].Message.Chat.ID != 42 || updates[0].Message.Text != "https://a.example/1" || updates[1].Callback.Data != "j:3" || updates[1].Callback.Message.Chat.ID != 42 {
		t.Fatalf("%+v %v", updates, err)
	}
	var poll map[string]any
	json.Unmarshal([]byte(strings.SplitN(seen[0], " ", 2)[1]), &poll)
	if poll["offset"] != float64(7) || poll["timeout"] != float64(25) {
		t.Fatalf("poll %v", poll)
	}
	if err = c.Send(ctx, 42, "hi", "MarkdownV2", [][]TGButton{{{Text: "5", Data: "r:1:5"}, {Text: "Картка", URL: "https://x.example/"}}}); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		ChatID      int64  `json:"chat_id"`
		Text        string `json:"text"`
		ParseMode   string `json:"parse_mode"`
		ReplyMarkup struct {
			Keyboard [][]map[string]string `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	json.Unmarshal([]byte(strings.SplitN(seen[1], " ", 2)[1]), &sent)
	row := sent.ReplyMarkup.Keyboard[0]
	if sent.ChatID != 42 || sent.ParseMode != "MarkdownV2" || row[0]["callback_data"] != "r:1:5" || row[1]["url"] != "https://x.example/" {
		t.Fatalf("%+v", sent)
	}
	if err = c.Answer(ctx, "c1", "ok"); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramClientErrorsNeverContainTheToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		if strings.Contains(r.URL.Path, "sendMessage") {
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: Character '.' is reserved"}`)
			return
		}
		io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	}))
	c := &TelegramClient{Token: testToken, BaseURL: server.URL, HTTP: server.Client()}
	if err := c.Send(context.Background(), 1, "x", "MarkdownV2", nil); !errors.Is(err, ErrTelegramBadMarkup) {
		t.Fatalf("%v", err)
	}
	_, err := c.Updates(context.Background(), 0, 0)
	if err == nil || !strings.Contains(err.Error(), "blocked") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("%v", err)
	}
	server.Close()
	_, err = c.Updates(context.Background(), 0, 0)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("a transport error must not leak the token: %v", err)
	}
}
