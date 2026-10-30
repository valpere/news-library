package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

type FilterRule struct {
	ID          string  `json:"id"`
	Reason      string  `json:"reason"`
	Field       string  `json:"field"`
	Contains    string  `json:"contains"`
	Probability float64 `json:"probability"`
}
type RuleSet struct {
	Threshold float64      `json:"threshold"`
	Rules     []FilterRule `json:"rules"`
}
type Contribution struct {
	Reason      string   `json:"reason"`
	Probability float64  `json:"probability"`
	RuleIDs     []string `json:"rule_ids"`
}

func NewRuleSet(threshold float64, rulesJSON string) (RuleSet, error) {
	r := RuleSet{Threshold: threshold, Rules: []FilterRule{}}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 || len(rulesJSON) > 64<<10 {
		return r, errors.New("некоректний поріг або завеликі правила")
	}
	if strings.TrimSpace(rulesJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(rulesJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&r.Rules); err != nil {
			return r, errors.New("NEWS_LIBRARY_FILTER_RULES має бути JSON-масивом правил")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return r, errors.New("зайві дані після правил")
		}
	}
	if strings.TrimSpace(rulesJSON) != "" {
		var raw []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(rulesJSON), &raw); err != nil {
			return r, errors.New("некоректні правила")
		}
		for _, rule := range raw {
			if v, ok := rule["probability"]; !ok || string(v) == "null" {
				return r, errors.New("probability правила має бути числом 0..1")
			}
		}
	}
	if len(r.Rules) > 100 {
		return r, errors.New("допустимо не більше 100 правил")
	}
	seen := map[string]bool{}
	for _, rule := range r.Rules {
		if strings.TrimSpace(rule.ID) == "" || seen[rule.ID] || len(rule.ID) > 100 || strings.TrimSpace(rule.Reason) == "" || len(rule.Reason) > 500 || strings.TrimSpace(rule.Contains) == "" || len(rule.Contains) > 1000 || math.IsNaN(rule.Probability) || math.IsInf(rule.Probability, 0) || rule.Probability < 0 || rule.Probability > 1 {
			return r, errors.New("некоректне правило: id, reason, contains та probability 0..1 обов’язкові")
		}
		if rule.Field != "url" && rule.Field != "title" && rule.Field != "text" {
			return r, errors.New("поле правила має бути url, title або text")
		}
		seen[rule.ID] = true
	}
	return r, nil
}
func (r RuleSet) evaluate(url string, snap *Snapshot) (float64, []Contribution) {
	values := map[string]string{"url": strings.ToLower(url)}
	if snap != nil {
		values["title"] = strings.ToLower(snap.Title)
		values["text"] = strings.ToLower(snap.Text)
	}
	groups := map[string]*Contribution{}
	for _, rule := range r.Rules {
		if !strings.Contains(values[rule.Field], strings.ToLower(rule.Contains)) {
			continue
		}
		reason := strings.ToLower(strings.TrimSpace(rule.Reason))
		c := groups[reason]
		if c == nil {
			c = &Contribution{Reason: reason}
			groups[reason] = c
		}
		c.Probability = math.Max(c.Probability, rule.Probability)
		c.RuleIDs = append(c.RuleIDs, rule.ID)
	}
	contributions := []Contribution{}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	remaining := 1.0
	for _, name := range names {
		c := groups[name]
		sort.Strings(c.RuleIDs)
		contributions = append(contributions, *c)
		remaining *= 1 - c.Probability
	}
	return 1 - remaining, contributions
}

// Configuration changes affect future processing, never silently reprocess items.
// The rules are one current set; the item keeps only the verdict and why it was given.
func (s *Store) ConfigureRules(r RuleSet) error {
	data, err := json.Marshal(r.Rules)
	if err != nil {
		return errors.New("некоректні правила")
	}
	validated, err := NewRuleSet(r.Threshold, string(data))
	if err != nil {
		return err
	}
	s.Rules = validated
	return nil
}

// filterReason is the one-line explanation stored on the item: the total risk, the threshold
// and the matched reasons with their rules. It is empty when no rule matched.
func filterReason(probability, threshold float64, contributions []Contribution) string {
	if len(contributions) == 0 {
		return ""
	}
	num := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	parts := make([]string, 0, len(contributions))
	for _, c := range contributions {
		parts = append(parts, fmt.Sprintf("%s: %s%% (правила %s)", c.Reason, strconv.FormatFloat(c.Probability*100, 'f', 0, 64), strings.Join(c.RuleIDs, ", ")))
	}
	return fmt.Sprintf("R=%s, поріг T=%s · %s", num(probability), num(threshold), strings.Join(parts, "; "))
}
func (s *Store) decideFilter(tx *sql.Tx, itemID int64, snap *Snapshot, override bool) (bool, error) {
	var url string
	if err := tx.QueryRow("SELECT canonical_url FROM items WHERE id=?", itemID).Scan(&url); err != nil {
		return false, err
	}
	probability, contributions := s.Rules.evaluate(url, snap)
	result := "passed"
	if probability > s.Rules.Threshold {
		result = "rejected"
	}
	if override {
		result = "overridden"
	}
	if _, err := tx.Exec("UPDATE items SET filter_state=?,filter_reason=? WHERE id=?", result, filterReason(probability, s.Rules.Threshold, contributions), itemID); err != nil {
		return false, err
	}
	if result == "rejected" {
		if _, err := tx.Exec("UPDATE items SET state='filtered',error='' WHERE id=?", itemID); err != nil {
			return false, err
		}
		if _, err := tx.Exec("UPDATE jobs SET state='cancelled',error='skipped by rules' WHERE item_id=? AND state IN ('queued','retry_wait')", itemID); err != nil {
			return false, err
		}
	}
	return result == "rejected", nil
}
func (s *Store) processSnapshot(tx *sql.Tx, itemID, sid int64, snap Snapshot, override bool) error {
	snap.ID = sid
	rejected, err := s.decideFilter(tx, itemID, &snap, override)
	if err != nil || rejected {
		return err
	}
	if contentErr := ValidateSourceContent(snap); contentErr != nil {
		_, err = tx.Exec("UPDATE items SET state='needs_input',error=? WHERE id=?", contentErr.Error(), itemID)
		return err
	}
	return s.queueDigest(tx, itemID, sid)
}

// FilterReason returns why the rules gave the item's current verdict ("" when no rule matched).
func (s *Store) FilterReason(itemID int64) (string, error) {
	var reason string
	err := s.DB.QueryRow("SELECT filter_reason FROM items WHERE id=?", itemID).Scan(&reason)
	return reason, err
}
func (s *Store) OverrideFilter(itemID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sid sql.NullInt64
	var state string
	if err = tx.QueryRow("SELECT (SELECT item_id FROM texts WHERE texts.item_id=items.id),filter_state FROM items WHERE id=?", itemID).Scan(&sid, &state); err != nil {
		return err
	}
	if state != "rejected" {
		return errors.New("матеріал уже дозволено до обробки")
	}
	if _, err = tx.Exec("UPDATE items SET filter_state='overridden',state='ready',error='' WHERE id=?", itemID); err != nil {
		return err
	}
	if sid.Valid {
		var artifacts int
		if err = tx.QueryRow("SELECT count(*) FROM digests WHERE item_id=?", sid).Scan(&artifacts); err != nil {
			return err
		}
		if artifacts > 0 && s.DigestConfig != nil {
			if err = s.queueRegeneration(tx, itemID, sid.Int64); err != nil {
				return err
			}
		} else {
			if s.DigestConfig != nil {
				if _, err = tx.Exec("UPDATE jobs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='' WHERE item_id=? AND stage='digest' AND config_version=? AND state IN ('failed','cancelled')", itemID, s.DigestConfig.Version()); err != nil {
					return err
				}
			}
			if err = s.queueDigest(tx, itemID, sid.Int64); err != nil {
				return err
			}
		}
	} else {
		if _, err = tx.Exec("INSERT INTO jobs(item_id,filter_override) VALUES(?,1)", itemID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE items SET state='queued' WHERE id=?", itemID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
