package library

import (
	"context"
	"strings"
)

type ChatCriteria struct {
	Query         *string   `json:"query,omitempty"`
	Category      *string   `json:"category,omitempty"`
	Tags          *[]string `json:"tags,omitempty"`
	Placement     *string   `json:"placement,omitempty"`
	Read          *string   `json:"read,omitempty"`
	Rating        *string   `json:"rating,omitempty"`
	State         *string   `json:"state,omitempty"`
	Filter        *string   `json:"filter,omitempty"`
	ConfidenceMin *string   `json:"confidence_min,omitempty"`
	ConfidenceMax *string   `json:"confidence_max,omitempty"`
	Sort          *string   `json:"sort,omitempty"`
	Direction     *string   `json:"direction,omitempty"`
	Mode          *string   `json:"mode,omitempty"`
}
type ChatCommand struct {
	Action         string       `json:"action"`
	Answer         string       `json:"answer,omitempty"`
	Criteria       ChatCriteria `json:"criteria,omitempty"`
	RemoveTags     []string     `json:"remove_tags,omitempty"`
	Question       string       `json:"question,omitempty"`
	Pending        string       `json:"pending,omitempty"`
	ItemAction     string       `json:"item_action,omitempty"`
	Index          int          `json:"index,omitempty"`
	Score          *int64       `json:"score,omitempty"`
	Tags           []string     `json:"tags,omitempty"`
	Limit          int          `json:"limit,omitempty"`
	IncludeUnknown bool         `json:"include_unknown,omitempty"`
}
type ChatCard struct {
	CanRate            bool
	Index              int
	Item               Item
	SourceID, DigestID int64
	Summary            string
}
type ChatReference struct{ ItemID, SourceID int64 }
type ChatState struct {
	Results         []ChatReference
	Total           int
	PendingMutation bool
	PendingText     string
	Filters         Filters
	Mode            string
	HasSearch       bool
	Offset          int
	Cards           []ChatCard
	Pending         string
	PendingCommand  ChatCommand
}
type ChatReply struct {
	Message, Kind string
	Cards         []ChatCard
	Actions       []ChatSuggestion
	Criteria      []string
	Total         int
	HasNext       bool
}
type ChatSuggestion struct{ Label, Event, Value string }
type ChatMessage struct {
	ID     int64
	User   string
	Reply  ChatReply
	Status string
}
type ChatSession struct {
	ID       string
	Revision int64
	State    ChatState
	Messages []ChatMessage
}
type ChatInput struct {
	Text, Event, Value, Token string
	Revision                  int64
}
type ChatPlanInput struct {
	Message     string              `json:"message"`
	State       ChatState           `json:"state"`
	History     []map[string]string `json:"history"`
	Vocabulary  map[string][]string `json:"vocabulary"`
	CurrentTime string              `json:"current_time"`
}
type ChatPlanner interface {
	PlanChat(context.Context, string, ChatPlanInput) (ChatCommand, error)
}
type ChatFacet struct {
	Name     string
	Count    int
	Selected bool
}

func chatCriteriaLabels(f Filters, mode string) []string {
	labels := []string{}
	rating := f.Rating
	if rating == "unrated" {
		rating = "неоцінені"
	}
	for _, p := range []struct{ name, value string }{{"Тема", f.Query}, {"Категорія", f.Category}, {"Розміщення", map[string]string{"inbox": "вхідні", "library": "бібліотека", "deferred": "відкладені"}[f.Placement]}, {"Читання", map[string]string{"0": "непрочитані", "1": "прочитані"}[f.Read]}, {"Власна оцінка", rating}, {"Стан", map[string]string{"queued": "у черзі", "fetching": "отримання тексту", "ready": "готові", "enriching": "аналізуються", "needs_input": "потрібен текст", "retry_wait": "очікують повтору", "digest_failed": "дайджест не завершено", "digest_retry_wait": "очікують повтору дайджесту", "filtered": "відсіяні правилами", "failed": "помилка"}[f.State]}, {"Правила", map[string]string{"passed": "пройшли відбір", "rejected": "відсіяні", "overridden": "повернуті до обробки", "not_checked": "ще не перевірені"}[f.Filter]}, {"Прогноз від, %", f.ConfidenceMin}, {"Прогноз до, %", f.ConfidenceMax}, {"Сортування", map[string]string{"added": "дата додавання", "rating": "власна оцінка", "confidence": "прогноз моделі", "relevance": "відповідність темі"}[f.Sort]}, {"Порядок", map[string]string{"asc": "зростання", "desc": "спадання"}[f.Direction]}} {
		if p.value != "" {
			labels = append(labels, p.name+": "+p.value)
		}
	}
	if len(f.Tags) > 0 {
		labels = append(labels, "Теги (будь-який): "+strings.Join(f.Tags, ", "))
	}
	if f.Query != "" {
		labels = append(labels, "Пошук: "+map[string]string{"fts": "за словами", "hybrid": "слова та зміст", "vector": "за змістом"}[mode])
	}
	return labels
}
func chatSuggestions() []ChatSuggestion {
	return []ChatSuggestion{{"Непрочитані вхідні", "inbox", ""}, {"Знайти матеріал", "find", ""}, {"Стан обробки", "queue", ""}, {"Повторити невдалі дайджести", "retry_digests", ""}, {"Дообробити наступні 5", "complete", "5"}, {"Що таке пошук за змістом?", "help", ""}}
}
