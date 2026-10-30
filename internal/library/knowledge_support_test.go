package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const supportCurrent = "The series introduces AI agents for defenders."
const supportOther = "AI agents use Prepare, Scan, Remediate and Monitor."

func supportGateway(t *testing.T, verification string, calls *int) *Ollama {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"models":[{"name":"fixture:cloud","remote_model":"fixture","remote_host":"https://ollama.com"}]}`)
			return
		}
		*calls++
		var payload struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		instruction := payload.Messages[0].Content
		reply := ""
		switch {
		case strings.Contains(instruction, "Окрема перевірка connection-support-v1"):
			var input struct {
				Article    KnowledgeContext   `json:"article"`
				Candidates []KnowledgeContext `json:"candidates"`
				Links      []KnowledgeLink    `json:"links"`
			}
			if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil {
				t.Error(err)
			}
			if len(input.Links) != 1 || !strings.Contains(input.Article.Text, supportCurrent) || len(input.Candidates) != 1 || !strings.Contains(input.Candidates[0].Text, supportOther) {
				t.Error("verification lost source attribution", input)
			}
			if len(input.Links) > 0 && input.Links[0].CurrentEvidence != supportCurrent {
				t.Error("wrong current evidence")
			}
			reply = verification
		case strings.Contains(instruction, "Визнач категорію"):
			reply = `{"category":"Зберігання даних","tags":["sqlite","backup"],"questions":["How do agents help defenders?"]}`
		default:
			var input struct {
				Article    KnowledgeContext   `json:"article"`
				Candidates []KnowledgeContext `json:"candidates"`
			}
			if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil {
				t.Error(err)
			}
			if len(input.Candidates) != 1 {
				t.Error("fixture needs one candidate")
				return
			}
			link := KnowledgeLink{ItemID: input.Candidates[0].ItemID, Title: "AI defense", Body: "The announcement introduces the same four-step framework as the implementation article.", Category: "Security", Tags: []string{"AI agents"}, Questions: []string{"Which four steps does the announcement introduce?"}, CurrentRole: "Four-step framework announcement", OtherRole: "Implementation", CurrentEvidence: supportCurrent, OtherEvidence: supportOther}
			b, _ := json.Marshal(map[string]any{"links": []KnowledgeLink{link}})
			reply = string(b)
		}
		json.NewEncoder(w).Encode(map[string]any{"done": true, "message": map[string]string{"content": reply}})
	}))
	t.Cleanup(server.Close)
	o, err := NewOllama(server.URL+"/api/chat", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestKnowledgeSupportRejectsAttributionAndIncompleteChecks(t *testing.T) {
	for _, reply := range []string{
		`{"version":"connection-support-v1","decisions":[{"item_id":2,"verdict":"unsupported","reason":"The announcement never states those four steps"}]}`,
		`{"version":"connection-support-v1","decisions":[{"item_id":2,"verdict":"uncertain","reason":"Insufficient evidence"}]}`,
		`{"version":"connection-support-v1","decisions":[]}`,
		`{"version":"connection-support-v1","decisions":[{"item_id":99,"verdict":"supported","reason":"Different candidate"}]}`,
		`{"version":"wrong","decisions":[{"item_id":2,"verdict":"supported","reason":"Wrong protocol"}]}`,
		`{"version":"connection-support-v1","decisions":[{"item_id":2,"verdict":"supported","reason":""}]}`,
		`{"version":"connection-support-v1","decisions":[{"item_id":2,"verdict":"supported","reason":"Ok"}],"extra":true}`,
		`not json`,
	} {
		t.Run(reply, func(t *testing.T) {
			calls := 0
			o := supportGateway(t, reply, &calls)
			links, err := o.Links(context.Background(), "fixture:cloud", KnowledgeContext{ItemID: 1, Text: supportCurrent}, []KnowledgeContext{{ItemID: 2, Text: supportOther}})
			var se *SourceError
			if len(links) != 0 || !errors.As(err, &se) || se.Kind != "format" || se.Retry || calls != 2 || !strings.Contains(se.Diagnostic, "## Link proposal") || !strings.Contains(se.Diagnostic, reply) {
				t.Fatal(links, err, calls)
			}
		})
	}
}

func TestKnowledgeSupportRequiresEveryProposedItemOnce(t *testing.T) {
	links := []KnowledgeLink{{ItemID: 2}, {ItemID: 3}}
	for _, decisions := range [][]knowledgeSupportDecision{
		{{2, "supported", "Source supports it"}, {3, "supported", "Source supports it"}},
		{{2, "supported", "Source supports it"}, {2, "supported", "Duplicate"}},
		{{2, "supported", "Source supports it"}},
		{{2, "supported", "Source supports it"}, {3, "yes", "Unknown verdict"}},
	} {
		err := validateKnowledgeSupport(links, knowledgeSupportResponse{Version: knowledgeSupportVersion, Decisions: &decisions})
		if (len(decisions) == 2 && decisions[1].ItemID == 3 && decisions[1].Verdict == "supported") != (err == nil) {
			t.Fatal(decisions, err)
		}
	}
}

func TestKnowledgeVerificationFailurePreservesCheckpointAndHumanHistory(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	seed := add(t, s, "https://example.com/seed")
	manual(t, s, seed, supportOther)
	completeFixtureDigest(t, s)
	knowledgeStep(t, &KnowledgeWorker{Store: s, Generator: &fixtureKnowledgeGenerator{}, Timeout: time.Second})
	id := add(t, s, "https://example.com/current")
	manual(t, s, id, supportCurrent)
	completeFixtureDigest(t, s)
	if err := s.Rate(id, 3, nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	// The seed owns item1; the proposal is from item2 to item1.
	reply := `{"version":"connection-support-v1","decisions":[{"item_id":1,"verdict":"unsupported","reason":"Announcement does not contain the four steps"}]}`
	w := &KnowledgeWorker{Store: s, Generator: supportGateway(t, reply, &calls), Timeout: time.Second}
	knowledgeStep(t, w)
	item, _ := s.Item(id)
	k, err := s.Knowledge(id)
	if err != nil || k == nil || item.State != "ready" {
		t.Fatal(item, k, err)
	}
	status, _ := s.KnowledgeStatus(id)
	if status.State != "failed" || status.Kind != "format" || calls != 3 {
		t.Fatal(status, calls)
	}
	notes, _ := s.Connections("", id)
	if len(notes) != 0 {
		t.Fatal("unsupported connection published", notes)
	}
	failures, err := s.KnowledgeFailures(id)
	if err != nil || len(failures) != 1 || !strings.Contains(failures[0].Response, "Content verification") {
		t.Fatal(failures, err)
	}
	if worked, err := w.Step(context.Background()); worked || err != nil || calls != 3 {
		t.Fatal("semantic failure auto-retried", worked, err, calls)
	}
	ratings, _ := s.Ratings(id)
	if len(ratings) != 1 {
		t.Fatal(ratings)
	}
}

func TestKnowledgeRecoveryVerifiesSavedProposalWithoutRegeneration(t *testing.T) {
	calls := 0
	o := supportGateway(t, `{"version":"connection-support-v1","decisions":[{"item_id":2,"verdict":"supported","reason":"A corrected relationship can be supported"}]}`, &calls)
	link := KnowledgeLink{ItemID: 2, Title: "AI agents", Body: "Both texts discuss AI agents, while only the implementation explains four steps.", Category: "Security", Tags: []string{"AI agents"}, Questions: []string{"How do agents help defenders?"}, CurrentRole: "General introduction", OtherRole: "Detailed process", CurrentEvidence: supportCurrent, OtherEvidence: supportOther}
	c := KnowledgeContext{ItemID: 1, Text: supportCurrent}
	other := KnowledgeContext{ItemID: 2, Text: supportOther}
	if err := validateKnowledgeLinks(c, []KnowledgeContext{other}, []KnowledgeLink{link}); err != nil {
		t.Fatal(err)
	}
	if err := o.verifyKnowledgeLinks(context.Background(), "fixture:cloud", c, []KnowledgeContext{other}, []KnowledgeLink{link}, "saved proposal"); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	// Empty proposals require no second request, including cancelled contexts.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.verifyKnowledgeLinks(ctx, "fixture:cloud", c, nil, nil, ""); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
}

type supportProposalFixture struct{ fixtureKnowledgeGenerator }

func (g *supportProposalFixture) Links(ctx context.Context, model string, c KnowledgeContext, others []KnowledgeContext) ([]KnowledgeLink, error) {
	link := KnowledgeLink{ItemID: others[0].ItemID, Title: "AI agents", Body: "Both texts discuss AI agents, while only the implementation explains four steps.", Category: "Security", Tags: []string{"AI agents"}, Questions: []string{"How do agents help defenders?"}, CurrentRole: "General introduction", OtherRole: "Detailed process", CurrentEvidence: supportCurrent, OtherEvidence: supportOther}
	raw, _ := json.Marshal(map[string]any{"links": []KnowledgeLink{link}})
	return nil, &SourceError{Kind: "format", Message: "captured proposal", Diagnostic: string(raw)}
}
func TestRecoveryCloudChecksCapturedProposalOnly(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	seed := add(t, s, "https://example.com/recovery-seed")
	manual(t, s, seed, supportOther)
	completeFixtureDigest(t, s)
	knowledgeStep(t, &KnowledgeWorker{Store: s, Generator: &fixtureKnowledgeGenerator{}, Timeout: time.Second})
	id := add(t, s, "https://example.com/recovery-current")
	manual(t, s, id, supportCurrent)
	completeFixtureDigest(t, s)
	w := &KnowledgeWorker{Store: s, Generator: &supportProposalFixture{}, Timeout: time.Second}
	knowledgeStep(t, w)
	before, _ := s.Knowledge(id)
	// Offline replay of the captured nonempty proposal is forbidden.
	c, _ := s.knowledgeContext(id)
	others, _ := s.knowledgeCandidates(c)
	offline := &KnowledgeRecoveryGenerator{Store: s}
	if links, err := offline.Links(context.Background(), "fixture:cloud", c, others); err == nil || len(links) != 0 {
		t.Fatal("unchecked replay accepted", links, err)
	}
	if err := s.RetryKnowledge(id); err != nil {
		t.Fatal(err)
	}
	calls := 0
	gateway := supportGateway(t, `{"version":"connection-support-v1","decisions":[{"item_id":1,"verdict":"supported","reason":"Both discuss agents; only the implementation gives the four steps"}]}`, &calls)
	recovery := &KnowledgeRecoveryGenerator{Store: s, Fallback: gateway}
	w.Generator = recovery
	knowledgeStep(t, w)
	after, _ := s.Knowledge(id)
	status, _ := s.KnowledgeStatus(id)
	notes, _ := s.Connections("", id)
	if status.State != "done" || before.ID != after.ID || calls != 1 || recovery.Replayed != 1 || recovery.Generated != 1 || len(notes) != 1 {
		t.Fatal(status, before, after, calls, recovery, notes)
	}
}

func TestHistoricalFooterCandidateDoesNotBlockValidArticle(t *testing.T) {
	s := backupStore(t)
	enableDigests(t, s)
	s.KnowledgeEnabled = true
	seed := knowledgeArticle(t, s, "old-valid-candidate")
	knowledgeStep(t, &KnowledgeWorker{Store: s, Generator: &fixtureKnowledgeGenerator{}, Timeout: time.Second})
	if err := s.SetTags(seed, []string{"sqlite"}); err != nil {
		t.Fatal(err)
	}
	manual(t, s, seed, "Copyright © 2026 Example Project")
	current := knowledgeArticle(t, s, "new-valid-current")
	generator := &fixtureKnowledgeGenerator{}
	knowledgeStep(t, &KnowledgeWorker{Store: s, Generator: generator, Timeout: time.Second})
	status, _ := s.KnowledgeStatus(current)
	if status.State != "done" || generator.MetadataCalls != 1 || generator.LinkCalls != 0 {
		t.Fatal(status, generator)
	}
}
