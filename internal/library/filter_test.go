package library

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func configureRules(t *testing.T, s *Store, threshold float64, rules []FilterRule) {
	t.Helper()
	data, _ := json.Marshal(rules)
	r, e := NewRuleSet(threshold, string(data))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ConfigureRules(r); e != nil {
		t.Fatal(e)
	}
}
func TestRulesDeduplicateCausesAndStrictThreshold(t *testing.T) {
	rules := []FilterRule{{ID: "a", Reason: " Same reason ", Field: "text", Contains: "ADVERTISEMENT", Probability: .6}, {ID: "b", Reason: "same reason", Field: "title", Contains: "offer", Probability: .5}, {ID: "c", Reason: "other reason", Field: "url", Contains: "/sale", Probability: .4}}
	data, _ := json.Marshal(rules)
	r, e := NewRuleSet(.76, string(data))
	if e != nil {
		t.Fatal(e)
	}
	p, c := r.evaluate("https://example.com/sale", &Snapshot{Title: "Offer", Text: "advertisement"})
	if math.Abs(p-.76) > 1e-9 || len(c) != 2 || len(c[1].RuleIDs) != 2 || c[1].Probability != .6 {
		t.Fatal("dependent causes counted twice", p, c)
	}
	// A match exactly on the configured threshold is allowed.
	s := testStore(t)
	configureRules(t, s, .6, rules[:1])
	id := add(t, s, "https://example.com/threshold")
	manual(t, s, id, "advertisement")
	i, _ := s.Item(id)
	if i.FilterState != "passed" {
		t.Fatal("non-strict threshold")
	}
	for _, raw := range []string{`[{"id":"x","reason":"x","field":"text","contains":"x"}]`, `[{"id":"x","reason":"x","field":"text","contains":"x","probability":null}]`, `[{"id":"x","reason":"x","field":"invalid","contains":"x","probability":.8}]`, `[] []`} {
		if _, e = NewRuleSet(.8, raw); e == nil {
			t.Fatal("invalid rules accepted", raw)
		}
	}
	if _, e = NewRuleSet(math.NaN(), "[]"); e == nil {
		t.Fatal("NaN threshold")
	}
}
func TestURLRejectZeroModelCallsAndExplicitOverride(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	configureRules(t, s, .8, []FilterRule{{ID: "sale", Reason: "advertising", Field: "url", Contains: "/sale", Probability: .95}})
	id := add(t, s, "https://example.com/sale")
	sourceCalls, modelCalls := 0, 0
	worker := Worker{Store: s, Source: sourceFunc(func(context.Context, string) (Snapshot, error) {
		sourceCalls++
		return Snapshot{Text: "Available source", Title: "Sale"}, nil
	}), Generator: generatorFunc(func(_ context.Context, j Job, s Snapshot) (Digest, error) {
		modelCalls++
		return Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture"}, nil
	}), Timeout: time.Second, Lease: time.Minute, Backoff: time.Second, MaxAttempts: 3}
	if worked, e := worker.Step(context.Background()); worked || e != nil || sourceCalls != 0 || modelCalls != 0 {
		t.Fatal("filtered URL processed", worked, e)
	}
	i, _ := s.Item(id)
	if i.FilterState != "rejected" || i.State != "filtered" || i.Score.Valid || i.Read {
		t.Fatal("rejection changed human decision", i)
	}
	if e := s.Retry(id); e == nil {
		t.Fatal("retry bypassed filter")
	}
	if e := s.OverrideFilter(id); e != nil {
		t.Fatal(e)
	}
	if e := s.OverrideFilter(id); e == nil {
		t.Fatal("duplicate override accepted")
	}
	for range 2 {
		if worked, e := worker.Step(context.Background()); !worked || e != nil {
			t.Fatal("override stalled", e)
		}
	}
	if sourceCalls != 1 || modelCalls != 1 {
		t.Fatal("override not bounded", sourceCalls, modelCalls)
	}
	i, _ = s.Item(id)
	if i.FilterState != "overridden" || i.State != "ready" || i.Score.Valid {
		t.Fatal("override lost state", i)
	}
	if reason, e := s.FilterReason(id); e != nil || reason == "" || !strings.Contains(reason, "T=") {
		t.Fatal("override lost the reason of the rejection", reason, e)
	}
}

type sourceFunc func(context.Context, string) (Snapshot, error)

func (f sourceFunc) Fetch(ctx context.Context, url string) (Snapshot, error) { return f(ctx, url) }
func TestSourceRejectKeepsTextSearchAndHumanHistory(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	configureRules(t, s, .8, []FilterRule{{ID: "promotion", Reason: "promotion", Field: "text", Contains: "advertisement", Probability: .9}})
	id := add(t, s, "https://example.com/source")
	if e := s.Rate(id, 3, nil); e != nil {
		t.Fatal(e)
	}
	s.SetRead(id, true)
	j := claim(t, s)
	if e := s.Complete(j, Snapshot{Text: "Searchable advertisement", Title: "Source"}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	worker := Worker{Store: s, Generator: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) { calls++; return Digest{}, nil }), Timeout: time.Second, Lease: time.Minute, MaxAttempts: 3}
	if worked, e := worker.Step(context.Background()); worked || e != nil || calls != 0 {
		t.Fatal("rejected source invoked model")
	}
	got, e := s.Search(Filters{Query: "Searchable", Filter: "rejected", Usefulness: "3", Read: "1"})
	if e != nil || len(got) != 1 {
		t.Fatal("filtered text not searchable", e)
	}
	snapshots, e := s.Snapshots(id)
	if e != nil || snapshots[0].Text != "Searchable advertisement" {
		t.Fatal("filter lost source", e)
	}
	if e = s.RetryDigest(id); e == nil {
		t.Fatal("digest retry bypassed reject")
	}
	if e = s.RegenerateDigest(id); e == nil {
		t.Fatal("regeneration bypassed reject")
	}
	before, _ := s.FilterReason(id)
	configureRules(t, s, .8, nil)
	after, _ := s.FilterReason(id)
	i, _ := s.Item(id)
	if i.FilterState != "rejected" || !i.Read || i.Score.Int64 != 3 || before == "" || before != after {
		t.Fatal("config change reprocessed judgments")
	}
	if e = s.OverrideFilter(id); e != nil {
		t.Fatal(e)
	}
	completeFixtureDigest(t, s)
	r, _ := s.Ratings(id)
	if len(r) != 1 || r[0].Score != 3 || r[0].AssessedAt.Valid {
		t.Fatal("history changed")
	}
}

func TestFilterKeepsOnlyCurrentVerdictAndReasonOnItem(t *testing.T) {
	s := testStore(t)
	configureRules(t, s, .5, []FilterRule{{ID: "ad-1", Reason: "Promotion", Field: "url", Contains: "promo", Probability: .9}})
	id := add(t, s, "https://example.com/promo")
	reason, err := s.FilterReason(id)
	if err != nil || !strings.Contains(reason, "promotion: 90%") || !strings.Contains(reason, "ad-1") || !strings.Contains(reason, "T=0.5") {
		t.Fatal(reason, err)
	}
	for _, table := range []string{"filter_decisions", "filter_rule_versions"} {
		if _, err = s.DB.Exec("SELECT 1 FROM " + table); err == nil {
			t.Fatal("history table still exists", table)
		}
	}
	clean := add(t, s, "https://example.com/plain")
	if reason, err = s.FilterReason(clean); err != nil || reason != "" {
		t.Fatal("reason without matches", reason, err)
	}
}
