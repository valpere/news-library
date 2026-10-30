package library

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestChatConversationPreservesPendingAndDoesNotWriteLibrary(t *testing.T) {
	if chatProcessingQuestion("Поясни що означає дообробка") != "" {
		t.Fatal("terminology question became diagnostics")
	}
	s, w := testWeb(t)
	w.ChatPlanner = chatPlannerFunc(func(_ context.Context, _ string, input ChatPlanInput) (ChatCommand, error) {
		if len(input.History) == 0 {
			t.Fatal("missing dialogue context")
		}
		return ChatCommand{Action: "answer", Answer: "Теги позначають окремі поняття. Категорія описує ширшу тему."}, nil
	})
	session, _ := chatTurn(t, w, ChatSession{}, "додай статтю", "", "")
	session, reply := chatTurn(t, w, session, "Поясни різницю між тегом і категорією", "", "")
	if !strings.Contains(reply.Message, "ширшу тему") || session.State.Pending != "add" {
		t.Fatal(reply, session.State)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM items").Scan(&n)
	if n != 0 {
		t.Fatal("answer wrote library", n)
	}
	_, reply = chatTurn(t, w, session, "https://example.com/after-explanation", "", "")
	if !strings.Contains(reply.Message, "Додано: 1") {
		t.Fatal(reply)
	}
	for _, answer := range []string{"", strings.Repeat("x", 6001)} {
		if validateChatCommand(ChatCommand{Action: "answer", Answer: answer}) == nil {
			t.Fatal("accepted invalid answer")
		}
	}
}

func TestChatExplainsRepeatedFailuresWithoutRetryOrModel(t *testing.T) {
	s, w := testWeb(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	id := knowledgeArticle(t, s, "diagnose")
	job, err := s.claimKnowledge(time.Second)
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	failure := &SourceError{Kind: "format", Message: "некоректний JSON аналізу", Diagnostic: "private response must not appear in chat"}
	if err = s.failKnowledge(*job, failure); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	job, err = s.claimKnowledge(time.Second)
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	if err = s.failKnowledge(*job, failure); err != nil {
		t.Fatal(err)
	}
	session, reply := chatTurn(t, w, ChatSession{}, "Чому вічно залишається незавершено? Що з ними таке?", "", "")
	for _, want := range []string{"завершилися помилкою 1", "не пройшла перевірку JSON", "вже мають дайджест"} {
		if !strings.Contains(reply.Message, want) {
			t.Fatal(want, reply)
		}
	}
	if strings.Contains(reply.Message, "private response") {
		t.Fatal("diagnostic leaked")
	}
	var active, events int
	s.DB.QueryRow("SELECT count(*) FROM knowledge_runs WHERE state IN ('queued','running','retry_wait')").Scan(&active)
	s.DB.QueryRow("SELECT count(*) FROM knowledge_runs WHERE kind<>''").Scan(&events)
	if active != 0 || events != 1 {
		t.Fatal("explanation retried", active, events)
	}
	session, reply = chatTurn(t, w, session, "скільки ще необроблених?", "", "")
	if !strings.Contains(reply.Message, "Помилкою завершилися 1") {
		t.Fatal(reply)
	}
	if _, err = s.QueueKnowledgeBatch(rand.Text(), 5, true); err != nil {
		t.Fatal(err)
	}
	_, reply = chatTurn(t, w, session, "чому аналіз не завершено?", "", "")
	if !strings.Contains(reply.Message, "у черзі 1") || strings.Contains(reply.Message, "кілька невдалих спроб") {
		t.Fatal("old failures reported as current", reply)
	}
}
