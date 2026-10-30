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

// recordFailure keeps the job's last failure only: a later failure replaces it and
// finishing the job deletes it. The replaced diagnostic file is left for removeUnusedFiles.
func (s *Store) recordFailure(tx *sql.Tx, j Job, f JobFailure) (replaced string, err error) {
	path, hash := "", ""
	if f.Response != "" {
		// The response reader is bounded; diagnostics never contain request headers.
		if len(f.Response) > 2<<20 {
			return "", errors.New("generation diagnostic too large")
		}
		if path, err = s.Files.Put("digests", []byte(f.Response)); err != nil {
			return "", err
		}
		hash = textHash(f.Response)
	}
	if err = tx.QueryRow(`SELECT response_path FROM jobs WHERE id=?`, j.ID).Scan(&replaced); err != nil {
		return "", err
	}
	_, err = tx.Exec(`UPDATE jobs SET failure_kind=?,failure_retryable=?,response_path=?,hash=? WHERE id=?`, f.Kind, f.Retryable, path, hash, j.ID)
	return replaced, err
}

// clearFailure forgets the job's last failure and returns its diagnostic file.
func (s *Store) clearFailure(tx *sql.Tx, jobID int64) (path string, err error) {
	if err = tx.QueryRow(`SELECT response_path FROM jobs WHERE id=?`, jobID).Scan(&path); err != nil {
		return "", err
	}
	_, err = tx.Exec(`UPDATE jobs SET failure_kind='',failure_retryable=0,response_path='',hash='' WHERE id=?`, jobID)
	return path, err
}

// JobFailures returns the job's last failure: zero or one element.
func (s *Store) JobFailures(jobID int64) ([]JobFailure, error) {
	f := JobFailure{JobID: jobID}
	var path, hash string
	err := s.DB.QueryRow(`SELECT failure_kind,failure_retryable,error,response_path,hash FROM jobs WHERE id=? AND failure_kind<>''`, jobID).Scan(&f.Kind, &f.Retryable, &f.Message, &path, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if path != "" {
		data, e := s.Files.Read(path, hash)
		if e != nil {
			f.DiagnosticError = e.Error()
		} else {
			f.Response = string(data)
		}
	}
	return []JobFailure{f}, nil
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
