package library

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type UsefulnessLevel struct {
	Score          int64
	Label, Meaning string
}

func usefulnessLevels() []UsefulnessLevel {
	return []UsefulnessLevel{
		{0, "Нульова", "Матеріал нічого корисного мені не дав"},
		{1, "Мінімальна", "Знайшов одну невелику корисну деталь"},
		{2, "Низька", "Трохи доповнив свої знання"},
		{3, "Помірна", "Краще зрозумів питання"},
		{4, "Висока", "Можу використати для конкретного завдання або рішення"},
		{5, "Дуже висока", "Суттєво вплинув на моє розуміння чи подальші дії"},
		{6, "Виняткова", "Став ключовим матеріалом, до якого повертатимусь"},
	}
}

func parseUsefulness(label string) (int64, bool) {
	label = strings.ToLower(strings.Join(strings.Fields(label), " "))
	for _, level := range usefulnessLevels() {
		if label == strings.ToLower(level.Label) {
			return level.Score, true
		}
	}
	return 0, false
}

func ratingLabel(score sql.NullInt64) string {
	if !score.Valid {
		return "Ще не оцінено"
	}
	if score.Int64 >= 0 && score.Int64 < 7 {
		return usefulnessLevels()[score.Int64].Label
	}
	return fmt.Sprintf("Оцінка поза шкалою: %d", score.Int64)
}

func usefulnessFilterLabel(raw string) string {
	if raw == "unrated" {
		return "Ще не оцінено"
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 && n <= 6 {
		return usefulnessLevels()[n].Label
	}
	return raw
}

// A model may extract a label only when the user's own rating request supplied
// it. Read-only searches/questions and embedded article instructions do not.
func chatExplicitUsefulness(text, label string) bool {
	want, ok := parseUsefulness(label)
	if !ok || readonlyChatPrefix(text) {
		return false
	}
	if got, ok := parseUsefulness(text); ok {
		return got == want
	}
	lower := strings.ToLower(strings.TrimRight(strings.TrimSpace(text), ".!"))
	for _, prefix := range []string{"оціни ", "оцінити ", "постав оцінку ", "дай оцінку "} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		object := strings.TrimSpace(strings.TrimPrefix(lower, prefix))
		for _, level := range usefulnessLevels() {
			value := strings.ToLower(level.Label)
			for _, separator := range []string{": ", " корисність ", " як "} {
				if !strings.HasSuffix(object, separator+value) {
					continue
				}
				name := strings.TrimSpace(strings.TrimSuffix(object, separator+value))
				if ratingUsefulnessObject.MatchString(name) {
					return level.Score == want
				}
			}
		}
	}
	return false
}

var ratingUsefulnessObject = regexp.MustCompile(`^(?:(?:перший|першу|перше|першого|другий|другу|друге|другого|третій|третю|третє|третього|четвертий|четверту|четверте|четвертого|п’ятий|п'ятий|п’яту|п'яту)(?:\s+(?:матеріал|матеріалу|статтю|картку))?|(?:№|номер|матеріал|картка|картку)\s*№?\s*[1-5])$`)

func usefulnessGuide() string {
	lines := []string{"Особиста корисність для вас зараз:"}
	for _, level := range usefulnessLevels() {
		lines = append(lines, level.Label+" — "+level.Meaning+".")
	}
	return strings.Join(lines, "\n") + "\nЩе не оцінено — окремий стан. Прочитання й корисність незалежні."
}
