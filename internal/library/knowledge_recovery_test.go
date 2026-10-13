package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type rejectedCombinedFixture struct{ fixtureKnowledgeGenerator }

func (g *rejectedCombinedFixture) Links(ctx context.Context, model string, c KnowledgeContext, others []KnowledgeContext) ([]KnowledgeLink, error) {
	links, err := g.fixtureKnowledgeGenerator.Links(ctx, model, c, others)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]any{"links": links, "category": c.Category, "tags": c.Tags, "questions": c.Questions})
	return nil, &SourceError{Kind: "format", Message: "некоректний JSON аналізу", Diagnostic: "```json\n" + string(raw) + "\n```"}
}
func TestKnowledgeRecoveryReplaysExactValidatedResponse(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	g := &rejectedCombinedFixture{}
	w := &KnowledgeWorker{Store: s, Generator: g, Timeout: time.Second}
	knowledgeArticle(t, s, "replay-seed")
	knowledgeStep(t, w)
	id := knowledgeArticle(t, s, "replay-current")
	knowledgeStep(t, w)
	item, _ := s.Item(id)
	status, _ := s.KnowledgeStatus(id, item.Selected.Int64)
	if status.State != "failed" {
		t.Fatal(status)
	}
	originalRun := status.ID
	before, _ := s.Knowledge(id, item.Selected.Int64)
	audit, err := s.AuditKnowledgeRecovery()
	if err != nil || len(audit) != 1 || !audit[0].Replayable {
		t.Fatal(audit, err)
	}
	if err := s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	replay := &KnowledgeRecoveryGenerator{Store: s}
	w.Generator = replay
	knowledgeStep(t, w)
	after, _ := s.Knowledge(id, item.Selected.Int64)
	status, _ = s.KnowledgeStatus(id, item.Selected.Int64)
	if status.ID != originalRun || status.State != "done" || before.ID != after.ID || before.Markdown != after.Markdown || replay.Replayed != 1 || replay.Generated != 0 {
		t.Fatal(status, before, after, replay)
	}
	failures, err := s.KnowledgeFailures(id, item.Selected.Int64)
	if err != nil || len(failures) != 1 || failures[0].Response == "" {
		t.Fatal("failure history lost", failures, err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM connection_members WHERE item_id=?", id).Scan(&count); err != nil || count == 0 {
		t.Fatal("replay did not publish validated links", err)
	}
}
func TestKnowledgeCombinedResponseStillRejectsUnsafeOrMalformedData(t *testing.T) {
	for _, raw := range []string{
		`{"links":[],"category":"Valid","tags":["tag"],"questions":["Why?"]}`,
		`{"links":[],"category":"Valid"}`,
		`{"links":[],"category":null,"tags":null,"questions":null}`,
		`{"links":[],"category":"Valid","tags":[],"questions":[]}`,
		`{"links":[],"unexpected":true}`,
		`{"links":[]} {"links":[]}`,
	} {
		var r knowledgeLinksResponse
		err := decodeKnowledgeReply(raw, &r)
		if err == nil {
			err = validateKnowledgeLinksResponse(KnowledgeContext{}, nil, r)
		}
		if raw == `{"links":[],"category":"Valid","tags":["tag"],"questions":["Why?"]}` {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted malformed extras", raw)
		}
	}
	c := KnowledgeContext{ItemID: 1, Text: "An exact source evidence phrase."}
	other := KnowledgeContext{ItemID: 2, Text: "Another exact source evidence phrase."}
	links := []KnowledgeLink{{ItemID: 2, Title: "Topic", Body: "Relation", Category: "Valid", Tags: []string{"tag"}, Questions: []string{"Why?"}, CurrentRole: "Current", OtherRole: "Other", CurrentEvidence: "An invented evidence phrase.", OtherEvidence: other.Text}}
	if err := validateKnowledgeLinksResponse(c, []KnowledgeContext{other}, knowledgeLinksResponse{Links: &links}); err == nil {
		t.Fatal("accepted invented evidence")
	}
}

func TestKnowledgeNumberedEvidenceIsVerbatimAndBounded(t *testing.T) {
	c := KnowledgeContext{ItemID: 1, Text: "## Heading\nAn exact current source excerpt."}
	other := KnowledgeContext{ItemID: 2, Text: "Another exact source excerpt."}
	one := 1
	l := KnowledgeLink{EvidenceVersion: KnowledgeEvidenceVersion, ItemID: 2, Title: "Topic", Body: "Relation", Category: "Valid", Tags: []string{"tag"}, Questions: []string{"Why?"}, CurrentRole: "Current", OtherRole: "Other", CurrentEvidenceID: &one, OtherEvidenceID: &one}
	links := []KnowledgeLink{l}
	if err := validateKnowledgeLinks(c, []KnowledgeContext{other}, links); err != nil {
		t.Fatal(err)
	}
	if links[0].CurrentEvidence != "An exact current source excerpt." || links[0].OtherEvidence != other.Text {
		t.Fatal(links)
	}
	zero := 0
	l.CurrentEvidenceID = &zero
	if err := validateKnowledgeLinks(c, []KnowledgeContext{other}, []KnowledgeLink{l}); err == nil {
		t.Fatal("accepted unknown evidence ID")
	}
	l.CurrentEvidenceID = &one
	l.CurrentEvidence = "An altered quotation."
	if err := validateKnowledgeLinks(c, []KnowledgeContext{other}, []KnowledgeLink{l}); err == nil {
		t.Fatal("accepted quote contradicting ID")
	}
	for _, e := range knowledgeEvidence(strings.Repeat("Дослівний текст. ", 1000)) {
		if !strings.Contains(strings.Repeat("Дослівний текст. ", 1000), e.Text) || !utf8.ValidString(e.Text) || len(e.Text) > 512 {
			t.Fatal(e)
		}
	}
}

func TestKnowledgeCloudSuppliesItsKnownEvidenceVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(rw, `{"models":[{"name":"fixture:cloud","remote_model":"fixture","remote_host":"https://ollama.com"}]}`)
			return
		}
		var payload struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if !strings.Contains(payload.Messages[1].Content, KnowledgeEvidenceVersion) {
			t.Error("input lacks evidence version")
		}
		json.NewEncoder(rw).Encode(map[string]any{"done": true, "message": map[string]string{"content": `{"links":[{"item_id":2,"title":"Topic","body":"Relation","category":"Valid","tags":["tag"],"questions":["Why?"],"current_role":"Current","other_role":"Other","current_evidence_id":1,"other_evidence_id":1}]}`}})
	}))
	defer server.Close()
	o, err := NewOllama(server.URL+"/api/chat", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := KnowledgeContext{ItemID: 1, Text: "Current exact source content."}
	other := KnowledgeContext{ItemID: 2, Text: "Other exact source content."}
	links, err := o.Links(context.Background(), "fixture:cloud", c, []KnowledgeContext{other})
	if err != nil || len(links) != 1 || links[0].CurrentEvidence != c.Text || links[0].OtherEvidence != other.Text || links[0].EvidenceVersion != KnowledgeEvidenceVersion {
		t.Fatal(links, err)
	}
}
