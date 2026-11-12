package library

import "testing"

func ptr(s string) *string { return &s }

func TestChatImpliedCriteriaFillsWhatThePlannerDropped(t *testing.T) {
	command := ChatCommand{Action: "search", Criteria: ChatCriteria{Sort: ptr("added"), Direction: ptr("desc")}}
	impliedChatCriteria("Знайти останні оброблені без оцінки", &command)
	c := command.Criteria
	if c.State == nil || *c.State != "ready" || c.Usefulness == nil || *c.Usefulness != "unrated" || *c.Sort != "added" || *c.Direction != "desc" {
		t.Fatalf("%+v", c)
	}
	command = ChatCommand{Action: "refine"}
	impliedChatCriteria("тільки непрочитані та неоцінені", &command)
	if c = command.Criteria; c.Read == nil || *c.Read != "0" || c.Usefulness == nil || *c.Usefulness != "unrated" {
		t.Fatalf("%+v", c)
	}
	command = ChatCommand{Action: "search"}
	impliedChatCriteria("найновіші готові матеріали", &command)
	if c = command.Criteria; c.State == nil || *c.State != "ready" || c.Sort == nil || *c.Sort != "added" || c.Direction == nil || *c.Direction != "desc" {
		t.Fatalf("%+v", c)
	}
}

func TestChatImpliedCriteriaLeavesPlannerChoicesAndOtherActionsAlone(t *testing.T) {
	command := ChatCommand{Action: "search", Criteria: ChatCriteria{Usefulness: ptr("3"), State: ptr("needs_input")}}
	impliedChatCriteria("оброблені без оцінки", &command)
	if *command.Criteria.Usefulness != "3" || *command.Criteria.State != "needs_input" {
		t.Fatalf("the planner's explicit values win: %+v", command.Criteria)
	}
	for _, c := range []ChatCommand{{Action: "answer"}, {Action: "item", ItemAction: "rate"}, {Action: "queue"}} {
		impliedChatCriteria("оброблені без оцінки", &c)
		if c.Criteria != (ChatCriteria{}) {
			t.Fatalf("%s must not get criteria: %+v", c.Action, c.Criteria)
		}
	}
	command = ChatCommand{Action: "refine"}
	impliedChatCriteria("прибери фільтр без оцінки", &command)
	if command.Criteria != (ChatCriteria{}) {
		t.Fatalf("removing a condition must not add it: %+v", command.Criteria)
	}
	command = ChatCommand{Action: "search"}
	impliedChatCriteria("прочитані статті про Go", &command)
	if command.Criteria.Read != nil || command.Criteria.State != nil {
		t.Fatalf("an ambiguous word adds nothing: %+v", command.Criteria)
	}
}
