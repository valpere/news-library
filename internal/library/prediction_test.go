package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enablePredictions(t *testing.T, s *Store, profile string) {
	t.Helper()
	if e := s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPredictionPromptVersion, MaxInputBytes: 4096, Interests: profile}); e != nil {
		t.Fatal(e)
	}
}
func storePredicted(t *testing.T, s *Store, id int64, probability float64) *Digest {
	t.Helper()
	manual(t, s, id, "Source with data for interest prediction")
	j := claim(t, s)
	if e := s.CompleteDigest(j, Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture", Prediction: &InterestPrediction{Probability: probability, Reason: "Matches the configured fixture interests", Profile: j.Interests}}); e != nil {
		t.Fatal(e)
	}
	d, e := s.DigestForSnapshot(j.SnapshotID.Int64)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestPredictionsSeparateFromJudgmentsAndSearch(t *testing.T) {
	s, w := testWeb(t)
	enablePredictions(t, s, "Fixture interests: Go and SQLite")
	a := add(t, s, "https://example.com/a")
	da := storePredicted(t, s, a, 0)
	b := add(t, s, "https://example.com/b")
	db := storePredicted(t, s, b, .9)
	c := add(t, s, "https://example.com/c")
	storePredicted(t, s, c, .9)
	// A source with no profile has no prediction, not a prediction of zero.
	enableDigests(t, s)
	d := add(t, s, "https://example.com/d")
	manual(t, s, d, "No current interests")
	completeFixtureDigest(t, s)
	if e := s.Rate(b, 3, nil); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{"asc", "desc"} {
		rows, e := s.Search(Filters{Sort: "confidence", Direction: dir})
		if e != nil || len(rows) != 4 || rows[3].ID != d {
			t.Fatal("null order", e)
		}
		if dir == "asc" && (rows[0].ID != a || rows[1].ID != b || rows[2].ID != c) {
			t.Fatal("stable prediction sort")
		}
		if dir == "desc" && (rows[0].ID != b || rows[1].ID != c || rows[2].ID != a) {
			t.Fatal("stable prediction sort")
		}
	}
	rows, e := s.Search(Filters{ConfidenceMin: "90", ConfidenceMax: "100", Usefulness: "3"})
	if e != nil || len(rows) != 1 || rows[0].ID != b {
		t.Fatal("confidence filter", e)
	}
	for _, f := range []Filters{{ConfidenceMin: "NaN"}, {ConfidenceMax: "101"}, {ConfidenceMin: "90", ConfidenceMax: "10"}} {
		if _, e = s.Search(f); e == nil {
			t.Fatal("invalid filter accepted", f)
		}
	}
	i, _ := s.Item(a)
	if i.FilterState != "passed" || i.State != "ready" || i.Score.Valid || !i.Confidence.Valid || i.Confidence.Float64 != 0 {
		t.Fatal("low prediction rejected item or set score", i)
	}
	rw := webRequest(w, "GET", "/items/2", nil, nil)
	if rw.Code != 200 || !strings.Contains(rw.Body.String(), "Confidence 90%") || !strings.Contains(rw.Body.String(), "Fixture interests") {
		t.Fatal("prediction card", rw.Code)
	}
	enablePredictions(t, s, "Different fixture profile")
	if e = s.RegenerateDigest(b, db.SnapshotID); e != nil {
		t.Fatal(e)
	}
	j := claim(t, s)
	if j.Interests != "Different fixture profile" {
		t.Fatal("profile not captured")
	}
	if e = s.CompleteDigest(j, Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture", Prediction: &InterestPrediction{Probability: .1, Reason: "A changed fixture profile predicts less interest", Profile: j.Interests}}); e != nil {
		t.Fatal(e)
	}
	old, e := s.Digest(db.ID, b)
	if e != nil || old.Prediction.Probability != .9 || old.Prediction.Profile != "Fixture interests: Go and SQLite" {
		t.Fatal("old prediction overwritten", e)
	}
	i, _ = s.Item(b)
	r, _ := s.Ratings(b)
	if i.Score.Int64 != 3 || i.Confidence.Float64 != .1 || len(r) != 1 {
		t.Fatal("prediction relinked human decision")
	}
	if da.Prediction.ProfileHash != profileHash(da.Prediction.Profile) {
		t.Fatal("profile provenance")
	}
}
func TestPredictedCloudResponseValidation(t *testing.T) {
	bad := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "fixture:cloud", "remote_model": "fixture", "remote_host": "https://ollama.com"}}})
			return
		}
		calls++
		var payload struct {
			Messages []map[string]string `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if !strings.Contains(payload.Messages[0]["content"], "JSON") || !strings.Contains(payload.Messages[1]["content"], "Fixture profile") {
			t.Error("missing prediction instruction/profile")
		}
		prediction := map[string]any{"probability": .73, "reason": "Matches the fixture profile topic"}
		if bad {
			prediction["probability"] = 2.0
		}
		body, _ := json.Marshal(map[string]any{"markdown": fixtureDigest, "prediction": prediction})
		json.NewEncoder(w).Encode(map[string]any{"done": true, "message": map[string]string{"content": string(body)}})
	}))
	defer server.Close()
	o, e := NewOllama(server.URL+"/api/chat", "", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	j := Job{Model: "fixture:cloud", PromptVersion: DigestPredictionPromptVersion, Interests: "Fixture profile", InputLimit: 4096}
	d, e := o.Generate(context.Background(), j, Snapshot{Text: "Source data"})
	if e != nil || d.Markdown != fixtureDigest || d.Prediction.Probability != .73 || calls != 1 {
		t.Fatal("prediction response", d, e)
	}
	bad = true
	if _, e = o.Generate(context.Background(), j, Snapshot{Text: "Source data"}); e == nil {
		t.Fatal("invalid probability accepted")
	}
	for _, raw := range []string{`{"markdown":"x","prediction":{"reason":"no probability"}}`, `{"markdown":"x","prediction":{"probability":null,"reason":"bad null"}}`, `{"markdown":"x","prediction":{"probability":0,"reason":5}}`, `{"markdown":"x","prediction":{"probability":0.4,"reason":"ok reason"}} {}`} {
		if _, _, e = parsePredictedDigest(raw, "Fixture profile"); e == nil {
			t.Fatal("invalid JSON accepted", raw)
		}
	}
	// A configured zero is valid and stays distinguishable from no prediction.
	raw, _ := json.Marshal(map[string]any{"markdown": fixtureDigest, "prediction": map[string]any{"probability": 0, "reason": "Explicit zero for this fixture topic"}})
	if _, p, e := parsePredictedDigest(string(raw), "Fixture profile"); e != nil || p == nil || p.Probability != 0 {
		t.Fatal("zero lost", e)
	}
}

func TestQueuedPredictionCapturesProfileAcrossRestart(t *testing.T) {
	s := testStore(t)
	enablePredictions(t, s, "Original fixture profile")
	id := add(t, s, "https://example.com/profile-restart")
	manual(t, s, id, "Original fixture text")
	path := filepath.Join(s.Files.Root, "library.db")
	s.Close()
	restarted, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	enablePredictions(t, restarted, "Changed fixture profile")
	j := claim(t, restarted)
	if j.Interests != "Original fixture profile" || j.PromptVersion != DigestPredictionPromptVersion {
		t.Fatal("pending job rerouted", j)
	}
	d := Digest{Markdown: fixtureDigest, ActualModel: j.Model, Provider: "fixture", Prediction: &InterestPrediction{Probability: .5, Reason: "Prediction based on an original fixture profile", Profile: "Changed fixture profile"}}
	if e = restarted.CompleteDigest(j, d); e == nil {
		t.Fatal("different profile accepted")
	}
	d.Prediction.Profile = j.Interests
	if e = restarted.CompleteDigest(j, d); e != nil {
		t.Fatal(e)
	}
	saved, e := restarted.DigestForSnapshot(j.SnapshotID.Int64)
	if e != nil || saved.Prediction.Profile != j.Interests {
		t.Fatal("profile provenance lost", e)
	}
}
