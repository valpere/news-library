package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// SplitText covers every byte, never splits a UTF-8 code point, and prefers
// paragraph/word boundaries near the end of each bounded chunk.
func SplitText(text string, limit int) ([]string, error) {
	if limit < 1024 || !utf8.ValidString(text) {
		return nil, errors.New("некоректний UTF-8 або ліміт частини")
	}
	var result []string
	for len(text) > limit {
		end := limit
		for !utf8.RuneStart(text[end]) {
			end--
		}
		if boundary := strings.LastIndexAny(text[:end], "\n \t"); boundary >= end*3/4 {
			end = boundary + 1
		}
		result = append(result, text[:end])
		text = text[end:]
	}
	if text != "" {
		result = append(result, text)
	}
	return result, nil
}
func textHash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }

type ChunkGenerator struct {
	Store *Store
	Leaf  Generator
}

func (g *ChunkGenerator) Generate(ctx context.Context, j Job, snap Snapshot) (Digest, error) {
	if j.ChunkLimit == 0 || len(snap.Text) <= j.InputLimit {
		return g.Leaf.Generate(ctx, j, snap)
	}
	if j.ChunkLimit < 1024 || j.ChunkLimit > j.InputLimit || j.ChunkMaxParts < 2 || j.ChunkMaxParts > 512 {
		return Digest{}, &SourceError{Kind: "configuration", Message: "некоректна конфігурація обробки частинами"}
	}
	parts, err := SplitText(snap.Text, j.ChunkLimit)
	if err != nil {
		return Digest{}, &SourceError{Kind: "configuration", Message: err.Error()}
	}
	if len(parts) > j.ChunkMaxParts {
		return Digest{}, &SourceError{Kind: "limit", Message: "текст перевищує ліміт кількості частин; повне джерело збережено"}
	}
	sourceParts := len(parts)
	totalInput, totalOutput := int64(0), int64(0)
	// Bound reduction calls and require progress, even if a provider expands notes.
	for round := 0; round < 8; round++ {
		if len(parts) > j.ChunkMaxParts {
			return Digest{}, &SourceError{Kind: "limit", Message: "перевищено ліміт частин етапу; джерело й готові частини збережені"}
		}
		notes := make([]string, 0, len(parts))
		for index, part := range parts {
			if err = ctx.Err(); err != nil {
				return Digest{}, err
			}
			d, e := g.Store.DigestCheckpoint(j, round, index, textHash(part))
			if e != nil {
				return Digest{}, e
			}
			if d == nil {
				subjob := j
				subjob.PromptVersion = DigestPromptVersion
				subjob.Interests = ""
				subjob.InputLimit = j.ChunkLimit
				subjob.ChunkLimit = 0
				source := snap
				source.Text = part
				source.Completeness = "partial"
				source.Warning = fmt.Sprintf("Частина %d із %d, етап %d. Це частина джерела або проміжних нотаток, не повний матеріал. %s", index+1, len(parts), round+1, snap.Warning)
				result, e := g.Leaf.Generate(ctx, subjob, source)
				if e != nil {
					return Digest{}, e
				}
				if _, e = SplitDigest(result.Markdown); e != nil {
					return Digest{}, digestFailureError(e, result.Markdown)
				}
				if len(result.Markdown) > j.ChunkLimit {
					return Digest{}, &SourceError{Kind: "limit", Message: "нотатки частини перевищують ліміт; джерело й попередні частини збережені"}
				}
				if e = g.Store.SaveDigestCheckpoint(j, round, index, textHash(part), result); e != nil {
					return Digest{}, e
				}
				d = &result
			}
			totalInput += d.InputTokens
			totalOutput += d.OutputTokens
			notes = append(notes, fmt.Sprintf("ЧАСТИНА %d/%d:\n%s", index+1, len(parts), d.Markdown))
		}
		combined := strings.Join(notes, "\n\n")
		if len(combined) <= j.InputLimit {
			source := snap
			source.Text = combined
			source.Warning = fmt.Sprintf("Дайджест синтезується з нотаток усіх %d частин збереженого джерела; не вважай проміжні нотатки новим першоджерелом. %s", sourceParts, snap.Warning)
			d, e := g.Leaf.Generate(ctx, j, source)
			if e != nil {
				return Digest{}, e
			}
			d.InputTokens += totalInput
			d.OutputTokens += totalOutput
			d.ChunkCount = sourceParts
			return d, nil
		}
		priorLength := 0
		for _, part := range parts {
			priorLength += len(part)
		}
		if len(combined) >= priorLength {
			return Digest{}, &SourceError{Kind: "limit", Message: "модель не скоротила нотатки для синтезу; повне джерело й частини збережені"}
		}
		parts, err = SplitText(combined, j.ChunkLimit)
		if err != nil {
			return Digest{}, err
		}
	}
	return Digest{}, &SourceError{Kind: "limit", Message: "перевищено кількість етапів скорочення; джерело й частини збережені"}
}
func (s *Store) DigestCheckpoint(j Job, round, part int, inputHash string) (*Digest, error) {
	var d Digest
	var path, hash, storedInput string
	err := s.DB.QueryRow("SELECT input_hash,markdown_path,hash,actual_model,provider,input_tokens,output_tokens FROM digest_checkpoints WHERE job_id=? AND round=? AND part=?", j.ID, round, part).Scan(&storedInput, &path, &hash, &d.ActualModel, &d.Provider, &d.InputTokens, &d.OutputTokens)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if storedInput != inputHash {
		return nil, errors.New("checkpoint does not match source/configuration")
	}
	data, err := s.Files.Read(path, hash)
	if err != nil {
		return nil, err
	}
	d.Markdown = string(data)
	return &d, nil
}
func (s *Store) SaveDigestCheckpoint(j Job, round, part int, inputHash string, d Digest) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = leaseValid(tx, j); err != nil {
		return err
	}
	path, err := s.Files.Put("digests", []byte(d.Markdown))
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO digest_checkpoints(job_id,round,part,input_hash,markdown_path,hash,actual_model,provider,input_tokens,output_tokens,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", j.ID, round, part, inputHash, path, textHash(d.Markdown), d.ActualModel, d.Provider, d.InputTokens, d.OutputTokens, nowUTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}
