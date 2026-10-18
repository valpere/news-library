package library

import (
	"strconv"
	"strings"
	"testing"
)

func TestNeedsUkrainianTranslation(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"AI agents help defenders detect threats.", true},
		{"Агенти допомагають захисникам виявляти загрози.", false},
		{"Це їжак, він живе в лісі.", false},
		{"Это простой текст, который съел ёжик.", true}, // Russian is translated too
		{"Привіт, world", true},                         // mostly Latin
		{"12345 — 67890", false},                        // nothing to translate
		{"Rust і Go: швидкий огляд мов програмування", false},
	}
	for _, c := range cases {
		if got := needsUkrainianTranslation(c.text); got != c.want {
			t.Errorf("%q: got %v want %v", c.text, got, c.want)
		}
	}
}

func TestTinyForeignSourceGetsTranslationJob(t *testing.T) {
	s := testStore(t)
	s.KnowledgeEnabled = true
	enableDigestsMin(t, s, 40)
	en := add(t, s, "https://example.com/en")
	manual(t, s, en, "AI agents help defenders detect threats.")
	uk := add(t, s, "https://example.com/uk")
	manual(t, s, uk, "Агенти допомагають захисникам виявляти загрози.")
	long := add(t, s, "https://example.com/long")
	manual(t, s, long, strings.Repeat("English words here. ", 30))
	if n := digestJobs(t, s, uk); n != 0 {
		t.Fatalf("Ukrainian tiny source needs nothing, got %d jobs", n)
	}
	var version, interests string
	if err := s.DB.QueryRow("SELECT prompt_version,interests FROM jobs WHERE item_id=? AND stage='digest'", en).Scan(&version, &interests); err != nil {
		t.Fatal(err)
	}
	if version != TranslationPromptVersion || interests != "" {
		t.Fatalf("tiny English source: %q %q", version, interests)
	}
	if err := s.DB.QueryRow("SELECT prompt_version FROM jobs WHERE item_id=? AND stage='digest'", long).Scan(&version); err != nil || version != DigestPromptVersion {
		t.Fatalf("long source keeps the digest: %q %v", version, err)
	}
	if i, _ := s.Item(en); i.State != "enriching" {
		t.Fatalf("translation in progress, state %q", i.State)
	}
}

func TestTranslationCompletesWithoutSectionsOrKnowledge(t *testing.T) {
	s := testStore(t)
	s.KnowledgeEnabled = true
	enableDigestsMin(t, s, 40)
	id := add(t, s, "https://example.com/en")
	manual(t, s, id, "AI agents help defenders detect threats.")
	j := claim(t, s)
	if err := s.CompleteDigest(j, Digest{Markdown: "ШІ-агенти допомагають захисникам виявляти загрози.", ActualModel: j.Model, Provider: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if i, _ := s.Item(id); i.State != "ready" {
		t.Fatalf("state %q", i.State)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM knowledge_runs WHERE item_id=?", id).Scan(&n)
	if n != 0 {
		t.Fatalf("translation must not start knowledge analysis, got %d runs", n)
	}
}

func TestDecodeTranslationAcceptsPlainUkrainianOnly(t *testing.T) {
	j := Job{PromptVersion: TranslationPromptVersion}
	if d, err := decodeDigestContent("  ШІ-агенти допомагають.  ", j); err != nil || d.Markdown != "ШІ-агенти допомагають." {
		t.Fatalf("%q %v", d.Markdown, err)
	}
	for _, bad := range []string{"", "   ", "AI agents help defenders."} {
		if _, err := decodeDigestContent(bad, j); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestTranslationPromptCarriesNoDigestStructure(t *testing.T) {
	msgs, err := digestMessages(Job{PromptVersion: TranslationPromptVersion, InputLimit: 4096}, Snapshot{Text: "AI agents help defenders."})
	if err != nil {
		t.Fatal(err)
	}
	sys := msgs[0]["content"]
	if !strings.Contains(sys, "Переклади") || strings.Contains(sys, "## Ключові тези") {
		t.Fatalf("unexpected system prompt:\n%s", sys)
	}
}

func TestTranslationPageShowsTranslationNotDigest(t *testing.T) {
	s, w := testWeb(t)
	enableDigestsMin(t, s, 40)
	id := add(t, s, "https://example.com/en")
	manual(t, s, id, "AI agents help defenders detect threats.")
	j := claim(t, s)
	if err := s.CompleteDigest(j, Digest{Markdown: "ШІ-агенти допомагають захисникам виявляти загрози.", ActualModel: j.Model, Provider: "fixture"}); err != nil {
		t.Fatal(err)
	}
	rw := webRequest(w, "GET", "/items/"+strconv.FormatInt(id, 10), nil, nil)
	body := rw.Body.String()
	if rw.Code != 200 || !strings.Contains(body, "Переклад українською") || !strings.Contains(body, "ШІ-агенти допомагають") {
		t.Fatalf("%d\n%s", rw.Code, body)
	}
	if strings.Contains(body, `id="digest"`) || strings.Contains(body, "Якість дайджесту") {
		t.Fatal("translation must not be presented as a digest")
	}
	if !strings.Contains(body, "/rating") {
		t.Fatal("rating must stay available")
	}
}
