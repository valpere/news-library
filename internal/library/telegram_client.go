package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrTelegramBadMarkup means Telegram refused the message's MarkdownV2.
var ErrTelegramBadMarkup = errors.New("telegram rejected the message markup")

type TGChat struct {
	ID int64 `json:"id"`
}
type TGMessage struct {
	MessageID int64  `json:"message_id"`
	Chat      TGChat `json:"chat"`
	Text      string `json:"text"`
}
type TGCallback struct {
	ID      string     `json:"id"`
	Data    string     `json:"data"`
	Message *TGMessage `json:"message"`
}
type TGUpdate struct {
	UpdateID int64       `json:"update_id"`
	Message  *TGMessage  `json:"message"`
	Callback *TGCallback `json:"callback_query"`
}

// TGButton is an inline keyboard button: a callback (Data) or a link (URL).
type TGButton struct{ Text, Data, URL string }

// TelegramAPI is the part of the Bot API the bot uses.
type TelegramAPI interface {
	Updates(ctx context.Context, offset int64, timeoutSeconds int) ([]TGUpdate, error)
	Send(ctx context.Context, chatID int64, text, parseMode string, keyboard [][]TGButton) error
	Answer(ctx context.Context, callbackID, text string) error
}

// TelegramClient calls the Bot API over HTTPS. The token is part of every request
// URL, so no error returned from here ever contains the URL.
type TelegramClient struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

func NewTelegramClient(token string) *TelegramClient {
	return &TelegramClient{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 70 * time.Second}}
}

func (c *TelegramClient) call(ctx context.Context, method string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: bad request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("telegram %s: unexpected response (HTTP %d)", method, resp.StatusCode)
	}
	if !envelope.OK {
		if strings.Contains(envelope.Description, "can't parse entities") {
			return ErrTelegramBadMarkup
		}
		return fmt.Errorf("telegram %s: %s", method, envelope.Description)
	}
	if result != nil {
		return json.Unmarshal(envelope.Result, result)
	}
	return nil
}

func (c *TelegramClient) Updates(ctx context.Context, offset int64, timeoutSeconds int) ([]TGUpdate, error) {
	var updates []TGUpdate
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeoutSeconds, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}

func (c *TelegramClient) Send(ctx context.Context, chatID int64, text, parseMode string, keyboard [][]TGButton) error {
	payload := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	if len(keyboard) > 0 {
		rows := make([][]map[string]string, len(keyboard))
		for i, row := range keyboard {
			for _, b := range row {
				button := map[string]string{"text": b.Text}
				if b.URL != "" {
					button["url"] = b.URL
				} else {
					button["callback_data"] = b.Data
				}
				rows[i] = append(rows[i], button)
			}
		}
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return c.call(ctx, "sendMessage", payload, nil)
}

func (c *TelegramClient) Answer(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text}, nil)
}
