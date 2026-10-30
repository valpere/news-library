package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestUsefulnessFilterAndSort(t *testing.T) {
	s := testStore(t)
	four := add(t, s, "https://example.com/new-four")
	s.Rate(four, 4, nil)
	zero := add(t, s, "https://example.com/zero")
	s.Rate(zero, 0, nil)
	unknown := add(t, s, "https://example.com/unknown")
	for value, want := range map[string]int64{"4": four, "0": zero, "unrated": unknown} {
		rows, err := s.Search(Filters{Usefulness: value})
		if err != nil || len(rows) != 1 || rows[0].ID != want {
			t.Fatal(value, rows, err)
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		rows, err := s.Search(Filters{Sort: "usefulness", Direction: direction})
		if err != nil || len(rows) != 3 || rows[2].ID != unknown {
			t.Fatal(err, rows)
		}
	}
	if _, err := s.Search(Filters{Usefulness: "7"}); err == nil {
		t.Fatal("invalid filter")
	}
}

func TestChatUsefulnessExplicitLabelsAndAllUnread(t *testing.T) {
	s, w := testWeb(t)
	for n := range 3 {
		id := add(t, s, fmt.Sprintf("https://example.com/usefulness/%d", n))
		manual(t, s, id, "Useful displayed source")
	}
	calls := 0
	w.ChatPlanner = chatPlannerFunc(func(_ context.Context, _ string, _ ChatPlanInput) (ChatCommand, error) {
		calls++
		return ChatCommand{Action: "item", ItemAction: "rate", Index: 1, Usefulness: "Висока"}, nil
	})
	session, reply := chatTurn(t, w, ChatSession{}, "", "inbox", "")
	if len(reply.Cards) != 3 {
		t.Fatal("unread excluded", reply)
	}
	session, reply = chatTurn(t, w, session, "", "item", "rate:1")
	if reply.Kind != "question" || len(reply.Actions) != 7 {
		t.Fatal(reply)
	}
	session, reply = chatTurn(t, w, session, "Дуже висока", "", "")
	history, err := s.Ratings(session.State.Cards[0].Item.ID)
	if err != nil || len(history) != 1 || history[0].Score != 5 || !history[0].AssessedAt.Valid || calls != 0 {
		t.Fatal(history, err, calls)
	}
	session, _ = chatTurn(t, w, session, "оціни другий матеріал: висока.", "", "")
	history, _ = s.Ratings(session.State.Cards[1].Item.ID)
	if len(history) != 1 || history[0].Score != 4 || calls != 0 {
		t.Fatal(history, calls)
	}
	// A model-proposed label for a read-only request is never human judgment.
	session, _ = chatTurn(t, w, session, "знайди висококорисні матеріали", "", "")
	history, _ = s.Ratings(session.State.Cards[0].Item.ID)
	if len(history) != 1 {
		t.Fatal("model invented a judgment", history)
	}
	for _, text := range []string{"оціни статтю з назвою корисність висока", "оціни перший матеріал: висока чи дуже висока", "знайди: висока", "наскільки це корисно?"} {
		if chatExplicitUsefulness(text, "Висока") {
			t.Fatal("ambiguous text supplied a level", text)
		}
	}
	state := ChatState{}
	label := "Висока"
	if err := applyChatCriteria(&state, ChatCommand{Action: "search", Criteria: ChatCriteria{Usefulness: &label}}); err != nil || state.Filters.Usefulness != "4" {
		t.Fatal(state, err)
	}
}

func TestWebUsefulnessAndReadAPIScale(t *testing.T) {
	s, w := testWeb(t)
	id := add(t, s, "https://example.com/scale-ui")
	manual(t, s, id, "Shown source")
	post(t, w, fmt.Sprintf("/items/%d/rating", id), url.Values{"score": {"4"}, "unknown_date": {"1"}})
	rw := webRequest(w, "GET", fmt.Sprintf("/items/%d", id), nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "корисність: Висока") || strings.Contains(rw.Body.String(), "value=\"deferred\"") || strings.Contains(rw.Body.String(), ">Оцінити 2<") {
		t.Fatal("old UI or missing label")
	}
	ratings, _ := s.Ratings(id)
	if len(ratings) != 1 || ratings[0].Score != 4 || ratings[0].AssessedAt.Valid {
		t.Fatal(ratings)
	}
	w.ReadToken = strings.Repeat("r", 32)
	rw = webRequest(w, "GET", fmt.Sprintf("/api/read/items/%d/context", id), nil, map[string]string{"Authorization": "Bearer " + w.ReadToken})
	var payload map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &payload); err != nil || rw.Code != 200 {
		t.Fatal(err, rw.Code)
	}
	if payload["rating"] != float64(4) || payload["rating_label"] != "Висока" {
		t.Fatal(payload)
	}
	before := len(ratings)
	rw = webRequest(w, "POST", fmt.Sprintf("/items/%d/rating", id), url.Values{"score": {"7"}}, nil)
	ratings, _ = s.Ratings(id)
	if rw.Code != 400 || len(ratings) != before {
		t.Fatal("invalid level mutated history")
	}
}
