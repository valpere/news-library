package library

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
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
type FilterDecision struct {
	ID, ItemID                             int64
	SnapshotID, OverrideOf                 sql.NullInt64
	RulesVersion, Stage, Result, CreatedAt string
	Probability, Threshold                 float64
	Contributions                          []Contribution
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
func (r RuleSet) version() (string, string) {
	data, _ := json.Marshal(r)
	return fmt.Sprintf("%x", sha256.Sum256(data)), string(data)
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
func (s *Store) ConfigureRules(r RuleSet) error {
	data, err := json.Marshal(r.Rules)
	if err != nil {
		return errors.New("некоректні правила")
	}
	validated, err := NewRuleSet(r.Threshold, string(data))
	if err != nil {
		return err
	}
	version, config := validated.version()
	if _, err = s.DB.Exec("INSERT INTO filter_rule_versions(version,config,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING", version, config, nowUTC()); err != nil {
		return err
	}
	s.Rules = validated
	return nil
}
func (s *Store) decideFilter(tx *sql.Tx, itemID int64, snap *Snapshot, override bool) (bool, error) {
	var url string
	if err := tx.QueryRow("SELECT canonical_url FROM items WHERE id=?", itemID).Scan(&url); err != nil {
		return false, err
	}
	probability, contributions := s.Rules.evaluate(url, snap)
	version, config := s.Rules.version()
	if _, err := tx.Exec("INSERT INTO filter_rule_versions(version,config,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING", version, config, nowUTC()); err != nil {
		return false, err
	}
	result, stage := "passed", "url"
	var sid any
	if probability > s.Rules.Threshold {
		result = "rejected"
	}
	if override {
		result = "overridden"
	}
	if snap != nil {
		stage = "source"
		sid = snap.ID
	}
	data, _ := json.Marshal(contributions)
	if _, err := tx.Exec(`INSERT INTO filter_decisions(item_id,snapshot_id,rules_version,stage,result,probability,threshold,contributions,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, itemID, sid, version, stage, result, probability, s.Rules.Threshold, string(data), nowUTC()); err != nil {
		return false, err
	}
	if _, err := tx.Exec("UPDATE items SET filter_state=? WHERE id=?", result, itemID); err != nil {
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
	return s.queueDigest(tx, itemID, sid)
}
func (s *Store) FilterDecisions(itemID int64) ([]FilterDecision, error) {
	rows, err := s.DB.Query("SELECT id,item_id,snapshot_id,rules_version,stage,result,probability,threshold,contributions,created_at,override_of FROM filter_decisions WHERE item_id=? ORDER BY id DESC", itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FilterDecision{}
	for rows.Next() {
		var d FilterDecision
		var data string
		if err = rows.Scan(&d.ID, &d.ItemID, &d.SnapshotID, &d.RulesVersion, &d.Stage, &d.Result, &d.Probability, &d.Threshold, &data, &d.CreatedAt, &d.OverrideOf); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &d.Contributions); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Store) OverrideFilter(itemID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sid sql.NullInt64
	var state string
	if err = tx.QueryRow("SELECT selected_snapshot,filter_state FROM items WHERE id=?", itemID).Scan(&sid, &state); err != nil {
		return err
	}
	if state != "rejected" {
		return errors.New("матеріал уже дозволено до обробки")
	}
	_, err = tx.Exec(`INSERT INTO filter_decisions(item_id,snapshot_id,rules_version,stage,result,probability,threshold,contributions,created_at,override_of) SELECT item_id,snapshot_id,rules_version,stage,'overridden',probability,threshold,contributions,?,id FROM filter_decisions WHERE item_id=? ORDER BY id DESC LIMIT 1`, nowUTC(), itemID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE items SET filter_state='overridden',state='ready',error='' WHERE id=?", itemID); err != nil {
		return err
	}
	if sid.Valid {
		var artifacts int
		if err = tx.QueryRow("SELECT count(*) FROM generated_artifacts WHERE snapshot_id=?", sid).Scan(&artifacts); err != nil {
			return err
		}
		if artifacts > 0 && s.DigestConfig != nil {
			if err = s.queueRegeneration(tx, itemID, sid.Int64); err != nil {
				return err
			}
		} else {
			if s.DigestConfig != nil {
				if _, err = tx.Exec("UPDATE jobs SET state='queued',attempts=0,next_attempt=0,lease_until=0,error='' WHERE item_id=? AND snapshot_id=? AND stage='digest' AND config_version=? AND state IN ('failed','cancelled')", itemID, sid, s.DigestConfig.Version()); err != nil {
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
