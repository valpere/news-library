package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var chatURLs = regexp.MustCompile(`https?://[^\s<>"']+`)

func extractChatURLs(text string) []string {
	urls := []string{}
	for _, raw := range chatURLs.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, ",;.!?")
		for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
			for strings.HasSuffix(raw, pair[1]) && strings.Count(raw, pair[1]) > strings.Count(raw, pair[0]) {
				raw = strings.TrimSuffix(raw, pair[1])
			}
		}
		urls = append(urls, raw)
	}
	return urls
}
func bareChatURLs(text string) bool {
	if len(extractChatURLs(text)) == 0 {
		return false
	}
	remaining := chatURLs.ReplaceAllString(text, "")
	for _, r := range remaining {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
func explicitChatMutation(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"знайд", "пошукай", "покажи", "поясни", "розкажи", "як ", "що ", "чому ", "чи ", "коли "} {
		if strings.HasPrefix(text, prefix) {
			return false
		}
	}
	for _, word := range []string{"додай", "додати", "добав", "збережи", "зберегти", "відклади", "відкласти", "познач", "прочитав", "прочитала", "оціни", "оцінити", "оцінка", "перезапусти", "перезапустити", "повтори", "повторити", "дооброби", "дообробити", "оброби", "обробити", "продовж", "дороби", "перебудуй", "перебудувати", "прибери тег"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}
func readonlyChatPrefix(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"знайд", "пошукай", "покажи", "поясни", "розкажи", "як ", "що ", "чому ", "чи ", "коли "} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}
func chatEventLabel(event, value string) string {
	labels := map[string]string{"inbox": "Покажи непрочитані вхідні", "find": "Знайти матеріал", "next": "Покажи наступні п’ять", "queue": "Покажи стан обробки", "retry_digests": "Повтори невдалі дайджести", "complete": "Дооброби статті", "help": "Що таке пошук за змістом?", "index": "Перебудуй пошук за змістом", "clear": "Скинь пошук", "category": "Категорія", "tag": "Тег пошуку", "item": "Дія з матеріалом"}
	text := labels[event]
	if text == "" {
		return ""
	}
	if event == "item" {
		parts := strings.Split(value, ":")
		if len(parts) >= 2 {
			text = map[string]string{"save": "Збережи до бібліотеки", "defer": "Відклади", "inbox": "Поверни до вхідних", "read": "Познач прочитаним", "unread": "Познач непрочитаним", "rate": "Оціни", "tags": "Додай теги"}[parts[0]] + " матеріал №" + parts[1]
			if len(parts) == 3 {
				text += " на " + parts[2]
			}
			return text
		}
	}
	if value != "" {
		text += " · " + value
	}
	return text
}
func chatLocalCommand(input ChatInput, state ChatState) (ChatCommand, bool, error) {
	c := ChatCommand{Action: input.Event}
	if input.Event != "" {
		switch input.Event {
		case "inbox", "next", "queue", "retry_digests", "help", "index":
		case "find":
			c = ChatCommand{Action: "clarify", Question: "Про що знайти матеріали? Можна вказати тему, категорію, теги, прочитане чи власну оцінку.", Pending: "search"}
		case "clear":
			c.Action = "reset_search"
		case "complete":
			n, e := strconv.Atoi(input.Value)
			if e != nil || n < 1 || n > 500 {
				return c, true, errors.New("розмір дообробки має бути 1..500")
			}
			c.Limit = n
		case "category":
			c.Action = "refine"
			value := input.Value
			if strings.EqualFold(state.Filters.Category, value) {
				value = ""
			}
			c.Criteria.Category = &value
		case "tag":
			c.Action = "refine"
			tags := append([]string{}, state.Filters.Tags...)
			found := false
			for i, t := range tags {
				if strings.EqualFold(t, input.Value) {
					tags = append(tags[:i], tags[i+1:]...)
					found = true
					break
				}
			}
			if !found && input.Value != "" {
				tags = append(tags, input.Value)
			}
			c.Criteria.Tags = &tags
		case "item":
			parts := strings.Split(input.Value, ":")
			if len(parts) < 2 || len(parts) > 3 {
				return c, true, errors.New("некоректна дія картки")
			}
			c.ItemAction = parts[0]
			var err error
			c.Index, err = strconv.Atoi(parts[1])
			if err != nil {
				return c, true, err
			}
			if len(parts) == 3 {
				score, e := strconv.ParseInt(parts[2], 10, 64)
				if e != nil {
					return c, true, e
				}
				c.Score = &score
			}
		default:
			return c, true, errors.New("невідома кнопка чату")
		}
		return c, true, validateChatCommand(c)
	}
	text := strings.TrimSpace(input.Text)
	lower := strings.ToLower(text)
	if state.PendingMutation && state.PendingCommand.Action != "clarify" && state.PendingCommand.Action != "" {
		pending := state.PendingCommand
		if lower == "так" || lower == "виконай" {
			return pending, true, nil
		}
		if pending.Action == "item" {
			if n, e := strconv.ParseInt(text, 10, 64); e == nil && n >= 0 {
				if pending.Index == 0 && n >= 1 && n <= int64(len(state.Cards)) {
					pending.Index = int(n)
					return pending, true, nil
				}
				if pending.ItemAction == "rate" && pending.Score == nil {
					pending.Score = &n
					return pending, true, nil
				}
			}
			if pending.ItemAction == "tags" && len(pending.Tags) == 0 {
				pending.Tags = strings.FieldsFunc(text, func(r rune) bool { return r == ',' })
				return pending, true, nil
			}
		}
	}

	urls := extractChatURLs(text)
	if len(urls) > 0 && (bareChatURLs(text) || (!readonlyChatPrefix(lower) && (strings.Contains(lower, "дод") || strings.Contains(lower, "добав") || strings.HasPrefix(lower, "ось посилання")))) {
		return ChatCommand{Action: "add"}, true, nil
	}
	switch lower {
	case "непрочитані", "непрочитані вхідні", "покажи непрочитані", "покажи непрочитані вхідні":
		return ChatCommand{Action: "inbox"}, true, nil
	case "далі", "наступні", "наступні 5", "покажи наступні", "покажи наступні 5":
		return ChatCommand{Action: "next"}, true, nil
	case "стан", "статус", "стан обробки", "покажи стан обробки":
		return ChatCommand{Action: "queue"}, true, nil
	case "скинь пошук", "новий пошук":
		return ChatCommand{Action: "reset_search"}, true, nil
	case "що таке пошук за змістом?", "що таке пошук за змістом", "пошук за змістом":
		return ChatCommand{Action: "help"}, true, nil
	case "додай статтю", "додати статтю", "додай відео":
		return ChatCommand{Action: "add"}, true, nil
	case "перезапусти", "повтори":
		return ChatCommand{Action: "clarify", Question: "Повторити невдалі дайджести чи продовжити дообробку статей?", Pending: "retry_kind"}, true, nil
	case "перезапусти невдалі дайджести", "повтори невдалі дайджести":
		return ChatCommand{Action: "retry_digests"}, true, nil
	case "дооброби наступні 5", "дооброби 5":
		return ChatCommand{Action: "complete", Limit: 5}, true, nil
	case "дооброби решту", "дооброби всі", "дооброби все":
		return ChatCommand{Action: "complete", Limit: 500}, true, nil
	}
	if state.Pending == "retry_kind" {
		if lower == "дайджести" {
			return ChatCommand{Action: "retry_digests"}, true, nil
		}
		if strings.Contains(lower, "дооброб") || lower == "метадані" {
			return ChatCommand{Action: "complete", Limit: 5}, true, nil
		}
	}
	if strings.HasPrefix(lower, "знайти:") || strings.HasPrefix(lower, "знайди:") {
		q := strings.TrimSpace(text[strings.Index(text, ":")+1:])
		mode := "fts"
		return ChatCommand{Action: "search", Criteria: ChatCriteria{Query: &q, Mode: &mode}}, true, nil
	}
	return c, false, nil
}
func applyChatCriteria(state *ChatState, c ChatCommand) error {
	if c.Action == "search" {
		state.Filters = Filters{}
		state.Mode = "hybrid"
		state.HasSearch = true
	}
	if c.Action == "refine" && !state.HasSearch {
		state.HasSearch = true
		state.Mode = "fts"
	}
	f := &state.Filters
	p := c.Criteria
	for _, pair := range []struct {
		from *string
		to   *string
		max  int
	}{{p.Query, &f.Query, 4096}, {p.Category, &f.Category, 600}, {p.Placement, &f.Placement, 32}, {p.Read, &f.Read, 8}, {p.Rating, &f.Rating, 24}, {p.State, &f.State, 32}, {p.Filter, &f.Filter, 32}, {p.ConfidenceMin, &f.ConfidenceMin, 16}, {p.ConfidenceMax, &f.ConfidenceMax, 16}, {p.Sort, &f.Sort, 32}, {p.Direction, &f.Direction, 8}, {p.Mode, &state.Mode, 16}} {
		if pair.from != nil {
			if len(*pair.from) > pair.max || !utf8.ValidString(*pair.from) {
				return errors.New("критерій пошуку завеликий")
			}
			*pair.to = strings.TrimSpace(*pair.from)
		}
	}
	if p.Tags != nil {
		tags, e := connectionList(*p.Tags, 20, 80)
		if e != nil {
			return e
		}
		f.Tags = tags
	}
	for _, tag := range c.RemoveTags {
		kept := []string{}
		for _, existing := range f.Tags {
			if !strings.EqualFold(existing, tag) {
				kept = append(kept, existing)
			}
		}
		f.Tags = kept
	}
	for _, pair := range []struct {
		value   string
		allowed []string
	}{{f.Placement, []string{"", "inbox", "library", "deferred"}}, {f.Read, []string{"", "0", "1"}}, {f.Filter, []string{"", "passed", "rejected", "overridden", "not_checked"}}, {f.State, []string{"", "queued", "fetching", "ready", "enriching", "needs_input", "retry_wait", "digest_failed", "digest_retry_wait", "filtered", "failed"}}, {f.Sort, []string{"", "added", "rating", "confidence", "relevance"}}, {f.Direction, []string{"", "asc", "desc"}}} {
		valid := false
		for _, v := range pair.allowed {
			if v == pair.value {
				valid = true
			}
		}
		if !valid {
			return errors.New("невідомий критерій пошуку")
		}
	}
	if f.Rating != "" && f.Rating != "unrated" {
		n, e := strconv.ParseInt(f.Rating, 10, 64)
		if e != nil || n < 0 {
			return errors.New("власна оцінка має бути невід'ємним цілим або «неоцінене»")
		}
	}
	if _, e := searchMode(state.Mode); e != nil {
		return e
	}
	state.Offset = 0
	return nil
}
func (w *Web) chatCards(refs []ChatReference) ([]ChatCard, error) {
	cards := []ChatCard{}
	for _, ref := range refs {
		item, err := w.Store.Item(ref.ItemID)
		if err != nil {
			return nil, err
		}
		if ref.SourceID == 0 && item.Selected.Valid {
			ref.SourceID = item.Selected.Int64
		}
		card := ChatCard{Item: item, SourceID: ref.SourceID}
		if ref.SourceID > 0 {
			source, sourceErr := w.Store.Snapshot(ref.SourceID)
			if sourceErr == nil && source.Title != "" {
				card.Item.Title = source.Title
			}
			if d, e := w.Store.DigestForSnapshot(ref.SourceID); e == nil && d != nil {
				if sections, e := SplitDigest(d.Markdown); e == nil && len(sections) > 0 {
					card.Summary = boundedKnowledgeText(sections[0].Markdown, 700)
					card.DigestID = d.ID
					card.CanRate = true
				}
			}
			if card.Summary == "" && sourceErr == nil {
				card.Summary = boundedKnowledgeText(source.Text, 700)
				card.CanRate = card.Summary != ""
			}
			if card.Summary == "" {
				card.Summary = "Збережений текст і дайджест недоступні; перевірте файли або backup."
			}
		} else {
			card.Summary = "Текст ще не отримано. Відкрийте картку, щоб доповнити його."
			if item.FilterState == "rejected" {
				card.Summary = "Матеріал відсіяно правилами. Причину та повернення до обробки дивіться в повній картці."
			}
		}

		card.Index = len(cards) + 1
		cards = append(cards, card)
	}
	return cards, nil
}
func (w *Web) chatSearch(r *http.Request, state *ChatState) (ChatReply, error) {
	result, err := w.search(r, state.Filters, state.Mode)
	if err != nil {
		return ChatReply{}, err
	}
	state.Offset = 0
	state.Total = len(result.Items)
	state.Results = []ChatReference{}
	for i, item := range result.Items {
		if i == 1000 {
			break
		}
		state.Results = append(state.Results, ChatReference{ItemID: item.ID, SourceID: chatResultSource(item)})
	}
	reply, err := w.chatPageResults(state)
	if result.Warning != "" {
		reply.Message += "\n" + result.Warning
	}
	if state.Total > 1000 {
		reply.Message += "\nДля перегляду збережено перші 1000 результатів; уточніть критерії для решти."
	}
	return reply, err
}
func (w *Web) chatPageResults(state *ChatState) (ChatReply, error) {
	start := min(state.Offset, len(state.Results))
	end := min(start+5, len(state.Results))
	cards, err := w.chatCards(state.Results[start:end])
	if err != nil {
		return ChatReply{}, err
	}
	state.Cards = cards
	reply := ChatReply{Cards: cards, Total: state.Total, HasNext: end < len(state.Results), Criteria: chatCriteriaLabels(state.Filters, state.Mode), Kind: "search"}
	if len(cards) == 0 {
		reply.Message = "Матеріалів за цими умовами немає. Спробуйте іншу тему або приберіть частину критеріїв."
	} else {
		reply.Message = fmt.Sprintf("Знайдено %d. Показую %d–%d.", state.Total, start+1, end)
	}
	if reply.HasNext {
		reply.Actions = append(reply.Actions, ChatSuggestion{"Наступні 5", "next", ""})
	}
	reply.Actions = append(reply.Actions, ChatSuggestion{"Скинути пошук", "clear", ""})
	return reply, nil
}
func (w *Web) executeChat(r *http.Request, session *ChatSession, command ChatCommand, input ChatInput) (ChatReply, error) {
	state := &session.State
	oldPendingMutation := state.PendingMutation
	pendingText := state.PendingText
	if err := validateChatCommand(command); err != nil {
		return ChatReply{}, err
	}
	writes := command.Action == "add" || command.Action == "item" || command.Action == "retry_digests" || command.Action == "complete" || command.Action == "index"
	if writes && input.Event == "" && !explicitChatMutation(input.Text) && !bareChatURLs(input.Text) && !(oldPendingMutation && !readonlyChatPrefix(input.Text)) {
		state.Pending = "item"
		if command.Action == "item" && command.ItemAction == "rate" {
			command.Score = nil
		}
		state.PendingCommand = command
		state.PendingMutation = true
		state.PendingText = ""
		if command.Action == "add" {
			state.PendingText = input.Text
		}
		return ChatReply{Kind: "question", Message: "Підтвердіть дію: " + chatCommandLabel(command) + ". Напишіть «так» або уточніть запит."}, nil
	}
	state.Pending = ""
	state.PendingMutation = false
	state.PendingText = ""
	state.PendingCommand = ChatCommand{}
	switch command.Action {
	case "clarify":
		state.Pending = command.Pending
		state.PendingCommand = command
		state.PendingMutation = input.Event != "" || explicitChatMutation(input.Text) || oldPendingMutation
		state.PendingText = strings.TrimSpace(pendingText + " " + input.Text)
		q := strings.TrimSpace(command.Question)
		if q == "" {
			q = "Що зробити: додати посилання, знайти матеріал, показати непрочитане чи стан обробки?"
		}
		return ChatReply{Kind: "question", Message: q}, nil
	case "add":
		urls := extractChatURLs(input.Text + " " + pendingText)
		if len(urls) == 0 {
			state.Pending = "add"
			return ChatReply{Kind: "question", Message: "Вставте посилання на статтю або YouTube — одне чи пачкою, через пробіли або переноси рядків."}, nil
		}
		rows, err := w.Store.AddURLs(strings.Join(urls, "\n"))
		if err != nil {
			return ChatReply{}, err
		}
		added, existing, failed := 0, 0, 0
		refs := []ChatReference{}
		issues := []string{}
		for _, row := range rows {
			switch row.Status {
			case "added":
				added++
			case "existing":
				existing++
			default:
				failed++
				if len(issues) < 5 {
					issues = append(issues, row.URL+": "+row.Error)
				}
			}
			if row.ItemID > 0 && len(refs) < 5 {
				item, e := w.Store.Item(row.ItemID)
				if e == nil {
					refs = append(refs, ChatReference{row.ItemID, item.Selected.Int64})
				}
			}
		}
		cards, err := w.chatCards(refs)
		if err != nil {
			return ChatReply{}, err
		}
		state.Cards = cards
		state.HasSearch = false
		state.Results = nil
		state.Total = 0
		state.Offset = 0
		return ChatReply{Kind: "result", Message: fmt.Sprintf("Додано: %d · Уже існує: %d · Не прийнято: %d. Прийняті посилання збережено. Стан обробки — на картках.", added, existing, failed) + func() string {
			if len(issues) > 0 {
				return "\n" + strings.Join(issues, "\n")
			}
			return ""
		}(), Cards: cards, Actions: []ChatSuggestion{{"Переглянути непрочитане", "inbox", ""}, {"Стан обробки", "queue", ""}}}, nil
	case "inbox":
		state.Filters = Filters{Placement: "inbox", Read: "0", Sort: "added", Direction: "asc"}
		state.Mode = "fts"
		state.HasSearch = true
		return w.chatSearch(r, state)
	case "search", "refine":
		if err := applyChatCriteria(state, command); err != nil {
			return ChatReply{}, err
		}
		return w.chatSearch(r, state)
	case "next":
		if !state.HasSearch {
			return ChatReply{Kind: "question", Message: "Спершу задайте тему пошуку або відкрийте непрочитані вхідні.", Actions: []ChatSuggestion{{"Непрочитані вхідні", "inbox", ""}}}, nil
		}
		state.Offset = min(state.Offset+5, len(state.Results))
		return w.chatPageResults(state)
	case "reset_search":
		state.Filters = Filters{}
		state.Mode = "fts"
		state.HasSearch = false
		state.Results = nil
		state.Cards = nil
		state.Total = 0
		return ChatReply{Message: "Критерії пошуку скинуто. Що знайти?", Kind: "question"}, nil
	case "help":
		return ChatReply{Message: "Пошук за змістом знаходить близькі ідеї, навіть якщо слова різні. Наприклад «як не втратити дані після аварії» може знайти матеріал про відновлення SQLite. Поєдную збіг слів із близькістю змісту; категорії, теги та непрочитане додатково звужують вибірку. Якщо індекс недоступний, покажу текстові збіги й повідомлю про це.", Actions: []ChatSuggestion{{"Знайти матеріал", "find", ""}, {"Стан індексу й черги", "queue", ""}, {"Перебудувати індекс", "index", ""}}}, nil
	case "queue":
		summary, err := w.Store.KnowledgeBatchSummary()
		if err != nil {
			return ChatReply{}, err
		}
		var sourcePending, digestPending, digestFailed, withoutText int
		for _, q := range []struct {
			sql string
			to  *int
		}{{"SELECT count(*) FROM jobs WHERE stage='fetch' AND state IN ('queued','running','retry_wait')", &sourcePending}, {"SELECT count(*) FROM jobs WHERE stage='digest' AND state IN ('queued','running','retry_wait')", &digestPending}, {"SELECT count(*) FROM items WHERE state='digest_failed'", &digestFailed}, {"SELECT count(*) FROM items WHERE selected_snapshot IS NULL", &withoutText}} {
			if err = w.Store.DB.QueryRow(q.sql).Scan(q.to); err != nil {
				return ChatReply{}, err
			}
		}
		reply := ChatReply{Message: fmt.Sprintf("Отримання тексту: %d · Дайджест у роботі: %d · Невдалих дайджестів: %d · Без тексту: %d.\nДообробка: ще не почато %d, незавершено %d, у роботі %d, завершено %d, потребують виправлення причини %d.\nПошук за змістом: %s.", sourcePending, digestPending, digestFailed, withoutText, summary.New, summary.Continue, summary.Active, summary.Done, summary.Blocked, w.Store.EmbeddingStatus()), Actions: []ChatSuggestion{{"Дообробити наступні 5", "complete", "5"}, {"Дообробити решту", "complete", "500"}, {"Повторити невдалі дайджести", "retry_digests", ""}}}
		if w.BackgroundPaused {
			reply.Message += "\nФонове виконання призупинено; задачі залишаються в черзі до запуску робітників."
		}
		return reply, nil
	case "complete":
		limit := command.Limit
		if limit == 0 {
			limit = 5
		}
		result, err := w.Store.QueueKnowledgeBatch(textHash(session.ID + ":" + input.Token)[:32], limit, true)
		if err != nil {
			return ChatReply{}, err
		}
		return ChatReply{Message: fmt.Sprintf("Дообробка: нових задач %d · Продовжено зі збереженої спроби %d · Пропущено через файли %d. Завершені та активні записи пропущено.", result.New, result.Resumed, result.Skipped), Actions: []ChatSuggestion{{"Стан обробки", "queue", ""}, {"Дообробити наступні 5", "complete", "5"}, {"Дообробити решту", "complete", "500"}}}, nil
	case "retry_digests":
		candidates, more, err := w.chatRetryCandidates()
		if err != nil {
			return ChatReply{}, err
		}
		selected := []DigestRetryCandidate{}
		blocked := 0
		limit := command.Limit
		if limit == 0 {
			limit = 500
		}
		for _, c := range candidates {
			if c.Selectable && (c.Checked || (c.Kind == "unknown" && command.IncludeUnknown && (strings.Contains(strings.ToLower(input.Text+" "+pendingText), "невідом") || strings.Contains(strings.ToLower(input.Text+" "+pendingText), "unknown")))) {
				if len(selected) < limit {
					selected = append(selected, c)
				}
			} else {
				blocked++
			}
		}
		if len(selected) == 0 {
			return ChatReply{Message: fmt.Sprintf("Дайджестів для такого повтору немає. %d записів потребують перевірки причини або явного вибору невідомих збоїв.", blocked), Actions: []ChatSuggestion{{"Стан обробки", "queue", ""}}}, nil
		}
		rows, err := w.Store.RetryFailedDigests(selected)
		if err != nil {
			return ChatReply{}, err
		}
		queued := 0
		for _, row := range rows {
			if row.Queued {
				queued++
			}
		}
		message := fmt.Sprintf("Повтор дайджестів: у чергу %d · Пропущено %d. Ще %d потребують перевірки причини. Збережені тексти не завантажуються заново.", queued, len(rows)-queued, blocked)
		if more {
			message += " Перевірено перші 5000 відмов; для решти доступний пакетний список."
		}
		return ChatReply{Message: message, Actions: []ChatSuggestion{{"Стан обробки", "queue", ""}}}, nil
	case "index":
		if w.Semantic == nil {
			return ChatReply{Message: "Пошук за змістом вимкнено в конфігурації. Пошук словами й метаданими доступний."}, nil
		}
		if err := w.Store.RebuildEmbeddings(); err != nil {
			return ChatReply{}, err
		}
		return ChatReply{Message: "Перебудову похідного індексу заплановано. Тексти, дайджести та власні оцінки збережено.", Actions: []ChatSuggestion{{"Стан індексу", "queue", ""}}}, nil
	case "item":
		if command.Index < 1 || command.Index > len(state.Cards) {
			state.Pending = "item"
			state.PendingCommand = command
			state.PendingMutation = true
			state.PendingText = input.Text
			return ChatReply{Kind: "question", Message: "Який матеріал із останньої добірки? Назвіть його номер або натисніть кнопку на картці."}, nil
		}
		card := state.Cards[command.Index-1]
		id := card.Item.ID
		if command.ItemAction == "rate" {
			explicitScore := command.Score != nil && (input.Event == "item" || (chatExplicitNumber(input.Text, *command.Score) || chatExplicitNumber(pendingText, *command.Score)))
			if !explicitScore {
				command.Score = nil
				state.Pending = "item"
				state.PendingCommand = command
				state.PendingMutation = true
				state.PendingText = input.Text
				return ChatReply{Kind: "question", Message: "Яку власну оцінку дати цьому матеріалу? Напишіть невід’ємне ціле число."}, nil
			}
			if card.SourceID == 0 || !card.CanRate {
				return ChatReply{Message: "Текст або дайджест цього матеріалу не було показано. Спершу відкрийте картку й доповніть текст або відновіть файли, щоб оцінити побачене."}, nil
			}
			var digest *int64
			if card.DigestID > 0 {
				digest = &card.DigestID
			}
			if err := w.Store.RateShown(id, *command.Score, &card.SourceID, digest, nil, "Оцінка через діалог"); err != nil {
				return ChatReply{}, err
			}
		} else {
			switch command.ItemAction {
			case "save", "defer", "inbox":
				placement := map[string]string{"save": "library", "defer": "deferred", "inbox": "inbox"}[command.ItemAction]
				if err := w.Store.SetPlacement(id, placement); err != nil {
					return ChatReply{}, err
				}
			case "read", "unread":
				if err := w.Store.SetRead(id, command.ItemAction == "read"); err != nil {
					return ChatReply{}, err
				}
			case "tags":
				if len(command.Tags) == 0 {
					state.Pending = "item"
					state.PendingCommand = command
					state.PendingMutation = true
					state.PendingText = input.Text
					return ChatReply{Kind: "question", Message: "Які теги додати до цього матеріалу?"}, nil
				}
				for _, tag := range command.Tags {
					if !strings.Contains(strings.ToLower(input.Text+" "+pendingText), strings.ToLower(tag)) {
						return ChatReply{Kind: "question", Message: "Назвіть явно теги, які хочете додати до статті."}, nil
					}
				}
				rows, err := w.Store.DB.Query("SELECT tag FROM item_tags WHERE item_id=? AND origin='human'", id)
				if err != nil {
					return ChatReply{}, err
				}
				tags := []string{}
				for rows.Next() {
					var t string
					if err = rows.Scan(&t); err != nil {
						rows.Close()
						return ChatReply{}, err
					}
					tags = append(tags, t)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return ChatReply{}, err
				}
				tags = append(tags, command.Tags...)
				if err = w.Store.SetTags(id, tags); err != nil {
					return ChatReply{}, err
				}
			}
		}
		refreshed, err := w.chatCards([]ChatReference{{id, card.SourceID}})
		if err != nil {
			return ChatReply{}, err
		}
		refreshed[0].Index = command.Index
		state.Cards[command.Index-1] = refreshed[0]
		return ChatReply{Message: "Дію з матеріалом виконано. Власні рішення збережені окремо від прогнозів моделі.", Cards: refreshed}, nil
	}
	return ChatReply{}, errors.New("невідома дія")
}
func (w *Web) handleChatTurn(r *http.Request, id string, input ChatInput) (ChatSession, ChatReply, error) {
	if !w.chatLock.TryLock() {
		return ChatSession{ID: id}, ChatReply{}, ErrChatBusy
	}
	defer w.chatLock.Unlock()
	timeout := w.ChatTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	session, turn, cached, err := w.Store.reserveChat(id, input, timeout+2*time.Minute)
	if err != nil {
		return session, ChatReply{}, err
	}
	if cached != nil {
		return session, *cached, nil
	}
	// Durable input is committed before any model or library operation.
	var before ChatState
	json.Unmarshal([]byte(jsonString(session.State)), &before)
	command, local, err := chatLocalCommand(input, session.State)
	if err == nil && !local {
		if w.ChatPlanner == nil {
			err = errors.New("мовна модель чату недоступна; скористайтеся кнопками або напишіть «Знайти: тема»")
		} else if len(input.Text) > 8192 {
			err = errors.New("неформальне повідомлення завелике; скоротіть текст (пачка URL може мати до 64 КіБ)")
		} else {
			loaded, e := w.Store.ChatSession(session.ID)
			err = e
			if err == nil {
				vocabulary, e := w.Store.chatVocabulary()
				err = e
				if err == nil {
					history := []map[string]string{}
					messages := loaded.Messages
					start := max(0, len(messages)-9)
					for _, message := range messages[start:] {
						if message.ID == turn {
							continue
						}
						history = append(history, map[string]string{"role": "user", "content": boundedKnowledgeText(message.User, 2048)}, map[string]string{"role": "assistant", "content": boundedKnowledgeText(message.Reply.Message, 1024)})
					}
					planState := session.State
					planState.Results = nil
					planState.Cards = append([]ChatCard(nil), planState.Cards...)
					for i := range planState.Cards {
						planState.Cards[i].Summary = ""
						planState.Cards[i].Item.MatchSnippet = ""
						planState.Cards[i].Item.Error = ""
						planState.Cards[i].Item.Title = boundedKnowledgeText(planState.Cards[i].Item.Title, 200)
						planState.Cards[i].Item.Tags = boundedKnowledgeText(planState.Cards[i].Item.Tags, 200)
						planState.Cards[i].Item.URL = boundedKnowledgeText(planState.Cards[i].Item.URL, 512)
						planState.Cards[i].Item.OriginalURL = boundedKnowledgeText(planState.Cards[i].Item.OriginalURL, 512)
					}
					ctx, cancel := context.WithTimeout(r.Context(), timeout)
					command, err = w.ChatPlanner.PlanChat(ctx, w.ChatModel, ChatPlanInput{Message: input.Text, State: planState, History: history, Vocabulary: vocabulary, CurrentTime: time.Now().In(w.zone).Format(time.RFC3339)})
					if err == nil {
						err = ctx.Err()
					}
					cancel()
				}
			}
		}
	}
	reply := ChatReply{}
	if err == nil {
		err = validateChatCommand(command)
	}
	if err == nil && r.Context().Err() != nil {
		err = r.Context().Err()
	}
	if err == nil {
		err = w.Store.applyingChat(turn)
	}
	if err == nil {
		reply, err = w.executeChat(r, &session, command, input)
	}
	if err != nil {
		session.State = before
		reply = ChatReply{Kind: "error", Message: err.Error(), Actions: chatSuggestions()}
	}
	if err = w.Store.finishChat(session, turn, reply); err != nil {
		return session, reply, err
	}
	session.Revision++
	return session, reply, nil
}

var chatScoreValue = regexp.MustCompile(`(?:на|оцінк[а-яіїєґ]*|бал[а-яіїєґ]*|score)\s*[:=]?\s*([-+]?\d+(?:[.,]\d+)?)`)

func chatExplicitNumber(text string, score int64) bool {
	if n, e := strconv.ParseInt(strings.TrimSpace(text), 10, 64); e == nil && n >= 0 && n == score {
		return true
	}
	for _, match := range chatScoreValue.FindAllStringSubmatch(strings.ToLower(text), -1) {
		if n, e := strconv.ParseInt(match[1], 10, 64); e == nil && n >= 0 && n == score {
			return true
		}
	}
	return false
}

func chatResultSource(item Item) int64 {
	if item.MatchSnapshot > 0 {
		return item.MatchSnapshot
	}
	return item.Selected.Int64
}
func chatCommandLabel(c ChatCommand) string {
	switch c.Action {
	case "add":
		return "додати надані посилання"
	case "complete":
		return "дообробити статті"
	case "retry_digests":
		return "повторити невдалі дайджести"
	case "index":
		return "перебудувати індекс пошуку"
	case "item":
		return fmt.Sprintf("%s для матеріалу №%d", map[string]string{"save": "зберегти до бібліотеки", "defer": "відкласти", "inbox": "повернути до вхідних", "read": "позначити прочитаним", "unread": "позначити непрочитаним", "rate": "дати власну оцінку", "tags": "додати теги"}[c.ItemAction], c.Index)
	}
	return c.Action
}

func (w *Web) chatRetryCandidates() ([]DigestRetryCandidate, bool, error) {
	all := []DigestRetryCandidate{}
	var after int64
	for len(all) < 5000 {
		page, more, e := w.Store.FailedDigestCandidates(after)
		if e != nil {
			return nil, false, e
		}
		all = append(all, page...)
		if !more {
			return all, false, nil
		}
		after = page[len(page)-1].ItemID
	}
	return all, true, nil
}
