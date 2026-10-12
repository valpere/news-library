package library

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type JobFailure struct {
	ID, JobID                                            int64
	Attempt                                              int
	Kind, Message, RecordedAt, Response, DiagnosticError string
	Retryable                                            bool
}

func FailureFromError(err error) JobFailure {
	f := JobFailure{Kind: "unknown", Message: err.Error()}
	var source *SourceError
	if errors.As(err, &source) {
		f.Kind = source.Kind
		f.Retryable = source.Retry
		f.Response = source.Diagnostic
		if f.Kind == "" {
			if source.Retry {
				f.Kind = "temporary"
			} else {
				f.Kind = "source"
			}
		}
	}
	switch f.Kind {
	case "temporary", "format", "configuration", "limit", "source", "unknown":
	default:
		f.Kind = "unknown"
	}
	return f
}
func (s *Store) FailError(j Job, err error, retry bool, maxAttempts int, now time.Time, backoff time.Duration) error {
	f := FailureFromError(err)
	switch f.Kind {
	case "format", "configuration", "limit", "source":
		retry = false
	}
	f.Retryable = retry
	return s.fail(j, err.Error(), retry, maxAttempts, now, backoff, &f)
}
func (s *Store) recordFailure(tx *sql.Tx, j Job, f JobFailure) error {
	path, hash := "", ""
	if f.Response != "" {
		// The response reader is bounded; diagnostics never contain request headers.
		if len(f.Response) > 2<<20 {
			return errors.New("generation diagnostic too large")
		}
		var err error
		path, err = s.Files.Put("digests", []byte(f.Response))
		if err != nil {
			return err
		}
		hash = textHash(f.Response)
	}
	_, err := tx.Exec(`INSERT INTO job_failures(job_id,attempt,kind,message,retryable,response_path,hash,recorded_at) VALUES(?,?,?,?,?,?,?,?)`, j.ID, j.Attempts, f.Kind, f.Message, f.Retryable, path, hash, nowUTC())
	return err
}
func (s *Store) JobFailures(jobID int64) ([]JobFailure, error) {
	rows, err := s.DB.Query(`SELECT id,job_id,attempt,kind,message,retryable,recorded_at,response_path,hash FROM job_failures WHERE job_id=? ORDER BY id DESC LIMIT 20`, jobID)
	if err != nil {
		return nil, err
	}
	type saved struct {
		f          JobFailure
		path, hash string
	}
	records := []saved{}
	for rows.Next() {
		var v saved
		if err = rows.Scan(&v.f.ID, &v.f.JobID, &v.f.Attempt, &v.f.Kind, &v.f.Message, &v.f.Retryable, &v.f.RecordedAt, &v.path, &v.hash); err != nil {
			break
		}
		records = append(records, v)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	out := []JobFailure{}
	for _, v := range records {
		if v.path != "" {
			data, e := s.Files.Read(v.path, v.hash)
			if e != nil {
				v.f.DiagnosticError = e.Error()
			} else {
				v.f.Response = string(data)
			}
		}
		out = append(out, v.f)
	}
	return out, nil
}
func FailureAdvice(kind string) (label, advice, button string) {
	button = "Повторити генерацію дайджесту"
	switch kind {
	case "temporary":
		return "Тимчасовий збій", "Мережа, час очікування або тимчасова відмова сервісу. Повтор пізніше може допомогти.", button
	case "format":
		return "Неприйнятний формат відповіді", "Модель відповіла, але структура не відповідає вимогам. Автоматичного повтору немає. Можна запросити інший варіант; якщо помилка повторюється, потрібна перевірка формату чи промпту.", "Запросити інший варіант дайджесту"
	case "configuration":
		return "Проблема налаштувань", "Перевірте модель, доступ або профіль. Незмінний запит не виправить налаштування.", "Повторити після зміни налаштувань"
	case "limit":
		return "Перевищено ліміт", "Перевірте ліміти розміру чи обробки частинами. Повтор без зміни умов не усуває причину.", "Повторити після зміни лімітів"
	case "source":
		return "Проблема джерела", "Перевірте доступність і збережений текст. Повтор генерації не відновлює відсутній текст.", "Повторити після перевірки тексту"
	}
	return "Причину не класифіковано", "Причина потребує перевірки. Повтор не гарантує виправлення.", button
}
func legacyFailureKind(message string) string {
	if strings.Contains(message, "секція дайджесту") || strings.Contains(message, "Формат відповіді моделі:") || message == "неправильна структура дайджесту" || message == "дайджест має починатися із секції" || message == "неповна або некоректна відповідь Ollama" {
		return "format"
	}
	return "unknown"
}
