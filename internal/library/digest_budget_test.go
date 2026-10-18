package library

import (
	"strings"
	"testing"
)

func digestSystemFor(t *testing.T, words int, version string) string {
	t.Helper()
	msgs, err := digestMessages(Job{PromptVersion: version, InputLimit: 1 << 20, Interests: "тест"},
		Snapshot{Text: strings.Repeat("слово ", words)})
	if err != nil {
		t.Fatal(err)
	}
	return msgs[0]["content"]
}

func TestShortSourceDigestHasProportionalBudget(t *testing.T) {
	for _, version := range []string{DigestPromptVersion, DigestPredictionPromptVersion} {
		short := digestSystemFor(t, 54, version)
		if !strings.Contains(short, "не більше 54 слів") || strings.Contains(short, "200–500 слів") {
			t.Errorf("%s: short source must get a budget equal to its size:\n%s", version, short)
		}
		long := digestSystemFor(t, 600, version)
		if !strings.Contains(long, "орієнтовно 200–500 слів") || strings.Contains(long, "не більше 600 слів") {
			t.Errorf("%s: long source keeps the standard size:\n%s", version, long)
		}
	}
}

func TestShortSourceBudgetBoundaryAndFloor(t *testing.T) {
	if s := digestSystemFor(t, shortSourceWords, DigestPromptVersion); !strings.Contains(s, "200–500 слів") {
		t.Error("exactly the threshold uses the standard size")
	}
	if s := digestSystemFor(t, shortSourceWords-1, DigestPromptVersion); !strings.Contains(s, "не більше") {
		t.Error("below the threshold uses the proportional budget")
	}
	// Four required sections cannot fit into a handful of words.
	if s := digestSystemFor(t, 8, DigestPromptVersion); !strings.Contains(s, "не більше 40 слів") {
		t.Errorf("tiny sources get the floor budget:\n%s", s)
	}
}

func TestPreviousPromptVersionIsRejected(t *testing.T) {
	_, err := digestMessages(Job{PromptVersion: "uk-sections-v1", InputLimit: 1 << 20}, Snapshot{Text: "текст"})
	if err == nil || !strings.Contains(err.Error(), "версія промпту") {
		t.Fatalf("%v", err)
	}
}
