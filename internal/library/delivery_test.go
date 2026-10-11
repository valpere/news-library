package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deliveryConfig() TelegramConfig {
	return TelegramConfig{Enabled: true, Token: "123:fixture-secret-token-1234567890", ChatID: "42", DailyTime: "09:00", Size: 2}
}
func deliveryFixture(t *testing.T) (*Store, int64) {
	t.Helper()
	s := backupStore(t)
	enableDigests(t, s)
	for _, path := range []string{"first", "second"} {
		id := add(t, s, "https://example.com/"+path)
		manual(t, s, id, "Saved source "+path)
		completeFixtureDigest(t, s)
	}
	id, err := s.PreviewDelivery(deliveryConfig(), "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, id
}

type fixtureSender struct {
	Calls, FailAt int
	Unknown       bool
}

func (f *fixtureSender) Send(context.Context, string, string) (int64, error) {
	f.Calls++
	if f.Calls == f.FailAt {
		return 0, &SendError{Unknown: f.Unknown}
	}
	return int64(100 + f.Calls), nil
}
func TestDeliveryFrozenPreviewPartialRetryAndHumanState(t *testing.T) {
	s, id := deliveryFixture(t)
	c := deliveryConfig()
	sender := &fixtureSender{FailAt: 2}
	preview, err := s.Delivery(id)
	if err != nil || len(preview.Parts) != 2 || !strings.Contains(preview.Parts[0].Text, fixtureDigest) {
		t.Fatal(preview, err)
	}
	if err = s.QueueDelivery(id, c, false); err != nil {
		t.Fatal(err)
	}
	// Second queue action is idempotent, not a second collection.
	if err = s.QueueDelivery(id, c, false); err != nil {
		t.Fatal(err)
	}
	worker := &DeliveryWorker{Store: s, Sender: sender, Config: c, Timeout: time.Second}
	for range 2 {
		if worked, err := worker.Step(context.Background()); err != nil || !worked {
			t.Fatal(worked, err)
		}
	}
	failed, _ := s.Delivery(id)
	if failed.State != "failed" || failed.Parts[0].State != "sent" || failed.Parts[1].State != "failed" {
		t.Fatal(failed)
	}
	if err = s.QueueDelivery(id, c, true); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent, _ := s.Delivery(id)
	if sent.State != "sent" || sender.Calls != 3 || sent.Parts[0].MessageID.Int64 != 101 {
		t.Fatal("resent confirmed part", sent, sender.Calls)
	}
	for _, part := range sent.Parts {
		item, _ := s.Item(part.ItemID)
		ratings, _ := s.Ratings(part.ItemID)
		if item.Read || item.Score.Valid || len(ratings) != 0 {
			t.Fatal("delivery changed human judgment")
		}
	}
	if _, err = s.PreviewDelivery(c, "manual", ""); !errors.Is(err, ErrEmptyDelivery) {
		t.Fatal("sent digest repeated", err)
	}
	backup := filepath.Join(t.TempDir(), "delivery-backup")
	if _, err = Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyBackup(backup); err != nil {
		t.Fatal("message blobs excluded from backup", err)
	}
}
func TestDeliveryUnknownRestartNeverRepeats(t *testing.T) {
	s, id := deliveryFixture(t)
	c := deliveryConfig()
	sender := &fixtureSender{FailAt: 1, Unknown: true}
	s.QueueDelivery(id, c, false)
	worker := &DeliveryWorker{Store: s, Sender: sender, Config: c, Timeout: time.Second}
	worker.Step(context.Background())
	unknown, _ := s.Delivery(id)
	if unknown.State != "unknown" {
		t.Fatal(unknown)
	}
	if err := s.QueueDelivery(id, c, true); err == nil {
		t.Fatal("unknown retried")
	}
	if worked, err := worker.Step(context.Background()); err != nil || worked || sender.Calls != 1 {
		t.Fatal(worked, err, sender.Calls)
	}
	// Simulate crash after persisted 'sending', before any durable outcome.
	s.DB.Exec("UPDATE deliveries SET state='sending' WHERE id=?", id)
	s.DB.Exec("UPDATE delivery_parts SET state='sending' WHERE delivery_id=? AND ordinal=0", id)
	if err := s.RecoverDeliveries(); err != nil {
		t.Fatal(err)
	}
	recovered, _ := s.Delivery(id)
	if recovered.State != "unknown" || recovered.Parts[0].State != "unknown" {
		t.Fatal(recovered)
	}
	if worked, err := worker.Step(context.Background()); err != nil || worked {
		t.Fatal("replayed interrupted request", worked, err)
	}
}
func TestDailyScheduleKyivSlotsAndNoCatchup(t *testing.T) {
	s, _ := deliveryFixture(t)
	c := deliveryConfig()
	worker := &DeliveryWorker{Store: s, Sender: &fixtureSender{}, Config: c, Timeout: time.Second}
	zone, _ := time.LoadLocation("Europe/Kyiv")
	due := time.Date(2026, 10, 11, 9, 0, 0, 0, zone)
	if err := worker.Schedule(due.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Deliveries()
	if len(before) != 1 {
		t.Fatal("sent before due")
	}
	if err := worker.Schedule(due); err != nil {
		t.Fatal(err)
	}
	if err := worker.Schedule(due.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Deliveries()
	if len(list) != 2 || list[0].State != "queued" {
		t.Fatal(list)
	}
	if err := worker.Schedule(due.Add(24*time.Hour + 10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Deliveries()
	if len(list) != 3 || list[0].State != "missed" {
		t.Fatal("catchup not suppressed", list)
	}
	// A manual draft cannot duplicate the same scheduled pending digests.
	if err := s.QueueDelivery(before[0].ID, c, false); err == nil {
		t.Fatal("queued digest duplicated by manual draft")
	}
}
func TestTelegramAdapterStatusesSecretSafetyAndMessageBounds(t *testing.T) {
	mode := "success"
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["chat_id"] != "42" || payload["parse_mode"] != nil {
			t.Error("unexpected message payload")
		}
		switch mode {
		case "success":
			rw.Write([]byte(`{"ok":true,"result":{"message_id":123}}`))
		case "failed":
			rw.WriteHeader(429)
			rw.Write([]byte(`{"ok":false,"error_code":429}`))
		case "ambiguous":
			rw.Write([]byte("invalid response"))
		}
	}))
	defer server.Close()
	telegram, err := NewTelegram(deliveryConfig().Token, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	telegram.base = server.URL
	if id, err := telegram.Send(context.Background(), "42", "fixture"); err != nil || id != 123 {
		t.Fatal(id, err)
	}
	for _, state := range []string{"failed", "ambiguous"} {
		mode = state
		_, err = telegram.Send(context.Background(), "42", "fixture")
		var send *SendError
		if !errors.As(err, &send) || send.Unknown != (state == "ambiguous") || strings.Contains(err.Error(), deliveryConfig().Token) {
			t.Fatal(err)
		}
	}
	bounded := telegramLimit(strings.Repeat("😀", 3000), 2600)
	units := 0
	for _, r := range bounded {
		units++
		if r > 0xffff {
			units++
		}
	}
	if units > 2600 || !strings.Contains(bounded, "Скорочено") {
		t.Fatal("invalid UTF16 message budget", units)
	}
	if (TelegramConfig{Token: "secret-without-colon"}).BotID() != "" {
		t.Fatal("invalid token leaked as bot ID")
	}
}
func TestDeliveryWebPreviewAndAgentMutationRejection(t *testing.T) {
	s, id := deliveryFixture(t)
	web, err := NewWeb(s, "http://127.0.0.1:8787", 4096)
	if err != nil {
		t.Fatal(err)
	}
	web.Telegram = deliveryConfig()
	response := webRequest(web, "GET", "/deliveries/"+fmtID(id), nil, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Надіслати в Telegram") || strings.Contains(response.Body.String(), deliveryConfig().Token) {
		t.Fatal("preview not safe", response.Code)
	}
	response = webRequest(web, "POST", "/deliveries/"+fmtID(id)+"/send", url.Values{"csrf": {web.CSRF}}, map[string]string{"Authorization": "Bearer agent"})
	if response.Code != 403 {
		t.Fatal("agent sent messages", response.Code)
	}
	response = webRequest(web, "POST", "/deliveries/"+fmtID(id)+"/send", url.Values{"csrf": {web.CSRF}}, nil)
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestRestoreQuarantinesUnfinishedDeliveryEvenIfQueued(t *testing.T) {
	s, id := deliveryFixture(t)
	s.QueueDelivery(id, deliveryConfig(), false)
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	restored := filepath.Join(root, "restored")
	if _, err := Backup(context.Background(), s.Files.Root, backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), backup, restored); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(filepath.Join(restored, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	delivery, err := copy.Delivery(id)
	if err != nil || delivery.State != "unknown" || delivery.Parts[0].State != "unknown" {
		t.Fatal(delivery, err)
	}
	sender := &fixtureSender{}
	worker := &DeliveryWorker{Store: copy, Sender: sender, Config: deliveryConfig(), Timeout: time.Second}
	if worked, err := worker.Step(context.Background()); err != nil || worked || sender.Calls != 0 {
		t.Fatal("restored delivery replayed", worked, err)
	}
}

func TestPreviewExcludesZeroReadAndRejectedWithoutInference(t *testing.T) {
	s, _ := deliveryFixture(t)
	s.Rate(1, 0, nil, nil, "not useful")
	s.SetRead(2, true)
	if _, err := s.PreviewDelivery(deliveryConfig(), "manual", ""); !errors.Is(err, ErrEmptyDelivery) {
		t.Fatal("human exclusions ignored", err)
	}
	s.SetRead(2, false)
	s.DB.Exec("UPDATE items SET filter_state='rejected' WHERE id=2")
	if _, err := s.PreviewDelivery(deliveryConfig(), "manual", ""); !errors.Is(err, ErrEmptyDelivery) {
		t.Fatal("rule exclusion ignored", err)
	}
}
