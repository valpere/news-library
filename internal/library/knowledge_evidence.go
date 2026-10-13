package library

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const KnowledgeEvidenceVersion = "exact-context-lines-v1"

type KnowledgeEvidence struct {
	ID   int
	Text string
}

// Only verbatim substrings from the exact bounded context are selectable.
func knowledgeEvidence(text string) []KnowledgeEvidence {
	out := []KnowledgeEvidence{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "Уривок джерела:" {
			continue
		}
		for len(line) > 0 && len(out) < 128 {
			end := min(len(line), 512)
			for end > 0 && !utf8.ValidString(line[:end]) {
				end--
			}
			part := strings.TrimSpace(line[:end])
			line = line[end:]
			if len(part) >= 12 {
				out = append(out, KnowledgeEvidence{ID: len(out) + 1, Text: part})
			}
		}
		if len(out) >= 128 {
			break
		}
	}
	return out
}
func resolveKnowledgeEvidence(c KnowledgeContext, others []KnowledgeContext, links []KnowledgeLink) error {
	byID := map[int64]string{}
	for _, other := range others {
		byID[other.ItemID] = other.Text
	}
	resolve := func(id *int, quote *string, text string) error {
		if id == nil {
			return nil
		}
		choices := knowledgeEvidence(text)
		if *id < 1 || *id > len(choices) {
			return errors.New("невідомий номер фрагмента підтвердження")
		}
		exact := choices[*id-1].Text
		if *quote != "" && *quote != exact {
			return errors.New("цитата не відповідає вибраному фрагменту")
		}
		*quote = exact
		return nil
	}
	for i := range links {
		if (links[i].CurrentEvidenceID != nil || links[i].OtherEvidenceID != nil) && links[i].EvidenceVersion != KnowledgeEvidenceVersion {
			return errors.New("непідтримувана версія нумерованих фрагментів")
		}
		other, ok := byID[links[i].ItemID]
		if !ok {
			return errors.New("зв'язок посилається на невідомий матеріал")
		}
		if err := resolve(links[i].CurrentEvidenceID, &links[i].CurrentEvidence, c.Text); err != nil {
			return err
		}
		if err := resolve(links[i].OtherEvidenceID, &links[i].OtherEvidence, other); err != nil {
			return err
		}
	}
	return nil
}
