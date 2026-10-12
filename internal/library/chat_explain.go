package library

import (
	"fmt"
	"strings"
)

// Processing questions are reads, including when the model is unavailable.
func chatProcessingQuestion(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	topic := false
	for _, word := range []string{"оброб", "черг", "заверш", "аналіз", "помил", "збій", "збо", "повтор"} {
		topic = topic || strings.Contains(text, word)
	}
	if !topic {
		return ""
	}
	for _, prefix := range []string{"чому", "що з", "що не так", "поясни чому", "поясни помил", "поясни збо", "поясни незаверш"} {
		if strings.HasPrefix(text, prefix) {
			return "diagnose"
		}
	}
	if strings.HasPrefix(text, "скільки") {
		return "queue"
	}
	return ""
}

func (w *Web) chatProcessingReply(explain bool) (ChatReply, error) {
	summary, err := w.Store.KnowledgeBatchSummary()
	if err != nil {
		return ChatReply{}, err
	}
	var fetching, digesting, digestFailed, withoutText int
	for _, q := range []struct {
		sql string
		to  *int
	}{
		{"SELECT count(*) FROM jobs WHERE stage='fetch' AND state IN ('queued','running','retry_wait')", &fetching},
		{"SELECT count(*) FROM jobs WHERE stage='digest' AND state IN ('queued','running','retry_wait')", &digesting},
		{"SELECT count(*) FROM items WHERE state='digest_failed'", &digestFailed},
		{"SELECT count(*) FROM items WHERE selected_snapshot IS NULL", &withoutText},
	} {
		if err = w.Store.DB.QueryRow(q.sql).Scan(q.to); err != nil {
			return ChatReply{}, err
		}
	}
	message := fmt.Sprintf("Зараз отримання тексту очікують або проходять %d матеріалів; дайджест готується для %d. Невдалих дайджестів: %d. Без збереженого тексту залишаються %d матеріалів — це не означає, що вони зараз стоять у черзі.\n\nАналіз категорій, тегів, питань і зв’язків: завершено %d; ще не почато %d; у черзі або виконується %d. Після попередніх спроб незавершеними залишаються %d; ще %d потребують виправлення причини перед повтором.", fetching, digesting, digestFailed, withoutText, summary.Done, summary.New, summary.Active, summary.Continue, summary.Blocked)
	candidates, err := knowledgeBatchCandidates(w.Store.DB)
	if err != nil {
		return ChatReply{}, err
	}
	failed, queued, running, waiting, repeated, invalidJSON := 0, 0, 0, 0, 0, 0
	kinds := map[string]int{}
	for _, c := range candidates {
		if c.Done {
			continue
		}
		switch c.State {
		case "queued":
			queued++
		case "running":
			running++
		case "retry_wait":
			waiting++
		case "failed":
			failed++
			kinds[c.Kind]++
			if c.Failures > 1 {
				repeated++
			}
			if c.Error == "некоректний JSON аналізу" {
				invalidJSON++
			}
		}
	}
	overview := message
	message = fmt.Sprintf("Докладніше про аналіз: у черзі %d, виконується %d, очікують повтору %d, завершилися помилкою %d.", queued, running, waiting, failed)
	if failed > 0 {
		message += " Незавершений аналіз не означає відсутність дайджесту: ці матеріали вже мають дайджест, але метадані чи зв’язки ще не готові."
	}
	if invalidJSON > 0 {
		message += fmt.Sprintf("\nДля %d матеріалів відповідь моделі не пройшла перевірку JSON аналізу. Це збій формату відповіді, а не очікування в черзі.", invalidJSON)
	}
	for _, kind := range []string{"format", "temporary", "configuration", "limit", "source", "unknown", ""} {
		if kinds[kind] == 0 {
			continue
		}
		label, advice, _ := FailureAdvice(kind)
		message += fmt.Sprintf("\n%s: %d. %s", label, kinds[kind], advice)
	}
	if repeated > 0 {
		message += fmt.Sprintf("\nДля %d матеріалів уже було кілька невдалих спроб. Лічильник не зменшується, коли повтор знову завершується помилкою. Спершу треба усунути причину, потім спробувати дообробити п’ять матеріалів і перевірити результат.", repeated)
	}
	if summary.New+summary.Active+summary.Continue+summary.Blocked == 0 {
		message += "\nУсі матеріали з готовими дозволеними дайджестами пройшли аналіз."
	}
	if !explain {
		message = overview
		if failed > 0 {
			message += fmt.Sprintf("\nПомилкою завершилися %d аналізів. Запитайте «Чому аналіз не завершено?», щоб побачити причини.", failed)
		}
	}
	if w.BackgroundPaused {
		message += "\nФонове виконання призупинено; задачі залишаються в черзі до запуску робітників."
	}
	if explain {
		message += "\n\n" + overview
	}
	message += "\nПошук за змістом: " + w.Store.EmbeddingStatus() + "."
	return ChatReply{Message: message, Actions: []ChatSuggestion{{"Дообробити наступні 5", "complete", "5"}, {"Дообробити решту", "complete", "500"}, {"Повторити невдалі дайджести", "retry_digests", ""}}}, nil
}
