package library

import "strings"

// impliedChatCriteria adds the filters that are unambiguous in the user's own words
// but that the language model left out (it can silently drop a condition, so a
// request like "останні оброблені без оцінки" would otherwise match everything).
// Values the planner already set are never changed, and a message that removes or
// resets conditions is left alone.
func impliedChatCriteria(text string, command *ChatCommand) {
	if command.Action != "search" && command.Action != "refine" {
		return
	}
	lower := strings.ToLower(text)
	lower = strings.TrimSpace(lower)
	for _, removal := range []string{"прибер", "скин", "зніми", "знімі", "забудь"} {
		if strings.HasPrefix(lower, removal) {
			return
		}
	}
	if strings.Contains(lower, "без обмежен") {
		return
	}
	// "необроблені", "не готові" mean the opposite of ready: add no state filter.
	for _, negated := range []string{"необроблен", "не оброблен", "неготов", "не готов"} {
		lower = strings.ReplaceAll(lower, negated, "")
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	c := &command.Criteria
	set := func(field **string, value string) {
		if *field == nil {
			*field = &value
		}
	}
	if has("без оцінки", "неоцінен", "не оцінен", "без моєї оцінки") {
		set(&c.Usefulness, "unrated")
	}
	if has("оброблен", "готов") {
		set(&c.State, "ready")
	}
	if has("непрочитан", "не прочитан") {
		set(&c.Read, "0")
	}
	if has("останні", "найновіш", "найсвіжіш", "свіжі", "нещодавн") {
		set(&c.Sort, "added")
		set(&c.Direction, "desc")
	}
}
