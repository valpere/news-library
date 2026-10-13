package library

import (
	"context"
	"encoding/json"
	"errors"
)

type knowledgeLinksResponse struct {
	Links     *[]KnowledgeLink `json:"links"`
	Category  json.RawMessage  `json:"category,omitempty"`
	Tags      json.RawMessage  `json:"tags,omitempty"`
	Questions json.RawMessage  `json:"questions,omitempty"`
}

func validateKnowledgeLinksResponse(c KnowledgeContext, candidates []KnowledgeContext, r knowledgeLinksResponse) error {
	if r.Links == nil {
		return errors.New("відсутній список links у відповіді аналізу")
	}
	if r.Category != nil || r.Tags != nil || r.Questions != nil {
		if r.Category == nil || r.Tags == nil || r.Questions == nil {
			return errors.New("неповна додаткова група метаданих у відповіді links")
		}
		k := ArticleKnowledge{}
		if err := json.Unmarshal(r.Category, &k.Category); err != nil {
			return err
		}
		if err := json.Unmarshal(r.Tags, &k.Tags); err != nil {
			return err
		}
		if err := json.Unmarshal(r.Questions, &k.Questions); err != nil {
			return err
		}
		if err := validateKnowledge(&k); err != nil {
			return err
		}
	}
	return validateKnowledgeLinks(c, candidates, *r.Links)
}

type KnowledgeRecoveryAudit struct {
	ItemID, RunID  int64
	Stage, Failure string
	Replayable     bool
}

// Recovery is opt-in. Normal ingestion never consults rejected diagnostics.
type KnowledgeRecoveryGenerator struct {
	Store               *Store
	Fallback            KnowledgeGenerator
	Replayed, Generated int
}

func (g *KnowledgeRecoveryGenerator) saved(model string, c KnowledgeContext) (string, error) {
	var path, hash string
	err := g.Store.DB.QueryRow(`SELECT f.response_path,f.hash FROM knowledge_failures f JOIN knowledge_runs r ON r.id=f.run_id WHERE r.item_id=? AND r.snapshot_id=? AND r.digest_id=? AND r.model=? AND r.prompt_version=? AND f.response_path<>'' ORDER BY f.id DESC LIMIT 1`, c.ItemID, c.SnapshotID, c.DigestID, model, KnowledgePromptVersion).Scan(&path, &hash)
	if err != nil {
		return "", err
	}
	raw, err := g.Store.Files.Read(path, hash)
	return string(raw), err
}
func (g *KnowledgeRecoveryGenerator) Metadata(ctx context.Context, model string, c KnowledgeContext, vocabulary map[string][]string) (ArticleKnowledge, error) {
	raw, err := g.saved(model, c)
	var r struct {
		Category  string   `json:"category"`
		Tags      []string `json:"tags"`
		Questions []string `json:"questions"`
	}
	k := ArticleKnowledge{}
	if err == nil {
		err = decodeKnowledgeReply(raw, &r)
		k = ArticleKnowledge{Category: r.Category, Tags: r.Tags, Questions: r.Questions}
		if err == nil {
			err = validateKnowledge(&k)
		}
	}
	if err == nil {
		g.Replayed++
		return k, nil
	}
	if g.Fallback != nil {
		g.Generated++
		return g.Fallback.Metadata(ctx, model, c, vocabulary)
	}
	return k, &SourceError{Kind: "format", Message: "Збережена відповідь не пройшла перевірку; потрібен явний хмарний повтор"}
}
func (g *KnowledgeRecoveryGenerator) Links(ctx context.Context, model string, c KnowledgeContext, candidates []KnowledgeContext) ([]KnowledgeLink, error) {
	raw, err := g.saved(model, c)
	var r knowledgeLinksResponse
	if err == nil {
		err = decodeKnowledgeReply(raw, &r)
	}
	if err == nil {
		err = validateKnowledgeLinksResponse(c, candidates, r)
	}
	if err == nil {
		g.Replayed++
		return *r.Links, nil
	}
	if g.Fallback != nil {
		g.Generated++
		return g.Fallback.Links(ctx, model, c, candidates)
	}
	return nil, &SourceError{Kind: "format", Message: "Збережена відповідь не пройшла перевірку; потрібен явний хмарний повтор"}
}

// Audit is read-only; callers may construct Store with a read-only DB handle.
func (s *Store) AuditKnowledgeRecovery() ([]KnowledgeRecoveryAudit, error) {
	candidates, err := knowledgeBatchCandidates(s.DB)
	if err != nil {
		return nil, err
	}
	out := []KnowledgeRecoveryAudit{}
	replay := KnowledgeRecoveryGenerator{Store: s}
	for _, candidate := range candidates {
		if candidate.State != "failed" {
			continue
		}
		a := KnowledgeRecoveryAudit{ItemID: candidate.ItemID, RunID: candidate.RunID, Failure: candidate.Error, Stage: "metadata"}
		c, err := s.knowledgeContext(candidate.ItemID, candidate.SnapshotID, candidate.DigestID)
		if err != nil {
			out = append(out, a)
			continue
		}
		k, err := s.knowledgeForRun(candidate.RunID)
		if err != nil {
			return nil, err
		}
		if k == nil {
			_, err = replay.Metadata(context.Background(), candidate.Model, c, nil)
		} else {
			a.Stage = "links"
			c.Category = k.Category
			c.Tags = k.Tags
			c.Questions = k.Questions
			others, e := s.knowledgeCandidates(c)
			if e != nil {
				return nil, e
			}
			if len(others) == 0 {
				err = nil
			} else {
				_, err = replay.Links(context.Background(), candidate.Model, c, others)
			}
		}
		a.Replayable = err == nil
		out = append(out, a)
	}
	return out, nil
}
