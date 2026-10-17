package library

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const freeFixture = "fixture/alpha:free"

func freeFixtureResponse(content string) map[string]any {
	return map[string]any{"model": freeFixture, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}, "usage": map[string]any{"cost": 0, "prompt_tokens": 15, "completion_tokens": 30, "is_byok": false}}
}
func freeFixtureCatalog(models ...string) map[string]any {
	data := []any{}
	for _, m := range models {
		data = append(data, map[string]any{"id": m, "pricing": map[string]string{"prompt": "0", "completion": "0", "request": "0", "image": "0"}})
	}
	return map[string]any{"data": data}
}
func freeFixtureJob() Job {
	return Job{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, InputLimit: 4096}
}
func testFreeRouter(t *testing.T, handler http.HandlerFunc, models ...string) *FreeOpenRouter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	r, err := NewFreeOpenRouter("private-test-key", models, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r.baseURL = server.URL
	return r
}
func TestOpenRouterZeroPriceAdmissionAndProvenance(t *testing.T) {
	get, post := 0, 0
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			get++
			if q.Header.Get("Authorization") != "" {
				t.Fatal("key leaked to catalog")
			}
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		post++
		if q.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Fatal("missing auth")
		}
		var p map[string]any
		if err := json.NewDecoder(q.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		provider := p["provider"].(map[string]any)
		for _, price := range provider["max_price"].(map[string]any) {
			if price != float64(0) {
				t.Fatal("nonzero budget")
			}
		}
		if p["model"] != freeFixture || provider["allow_fallbacks"] != false || provider["require_parameters"] != true || p["stream"] != false || p["tools"] != nil || p["plugins"] != nil || p["models"] != nil {
			t.Fatal("routing widened", p)
		}
		json.NewEncoder(w).Encode(freeFixtureResponse(fixtureDigest))
	}, freeFixture)
	d, err := r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source text"})
	if err != nil || d.Provider != "openrouter-free" || d.ActualModel != freeFixture || d.Model != "fixture:cloud" || d.Markdown != fixtureDigest || d.InputTokens != 15 || get != 1 || post != 1 {
		t.Fatal(d, err, get, post)
	}
}
func TestOpenRouterRejectsPaidUnknownAndTinyPricesBeforeGeneration(t *testing.T) {
	for _, price := range []string{`"0.01"`, `"1e-10000"`, `null`, `"unknown"`, `"0/1"`, `"0e1000000000"`} {
		t.Run(price, func(t *testing.T) {
			post := 0
			r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "POST" {
					post++
					t.Fatal("unpriced model generated")
				}
				w.Write([]byte(`{"data":[{"id":"fixture/alpha:free","pricing":{"prompt":"0","completion":"0","request":` + price + `}}]}`))
			}, freeFixture)
			_, err := r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"})
			var se *SourceError
			if !errors.As(err, &se) || se.Kind != "configuration" || post != 0 {
				t.Fatal(err, post)
			}
		})
	}
	for _, raw := range []string{`["provider/paid"]`, `["openrouter/random:free"]`, `["provider/a:free","provider/a:free"]`, `null`, `["provider/a:free:floor"]`} {
		if _, err := ParseFreeModels(raw); err == nil {
			t.Fatal("invalid allowlist accepted", raw)
		}
	}
}
func TestOpenRouterRejectedRepliesNeverBecomeDigests(t *testing.T) {
	for _, kind := range []string{"cost", "missing_cost", "byok", "wrong_model", "truncated", "refusal", "tools", "invalid_digest", "error"} {
		t.Run(kind, func(t *testing.T) {
			response := freeFixtureResponse(fixtureDigest)
			usage := response["usage"].(map[string]any)
			choice := response["choices"].([]any)[0].(map[string]any)
			message := choice["message"].(map[string]any)
			switch kind {
			case "cost":
				usage["cost"] = 0.1
			case "missing_cost":
				delete(usage, "cost")
			case "byok":
				usage["is_byok"] = true
			case "wrong_model":
				response["model"] = "provider/paid"
			case "truncated":
				choice["finish_reason"] = "length"
			case "refusal":
				message["refusal"] = "no"
			case "tools":
				message["tool_calls"] = []any{map[string]any{"id": "call"}}
			case "invalid_digest":
				message["content"] = "hello"
			case "error":
				response["error"] = map[string]any{"code": 500}
			}
			raw, _ := json.Marshal(response)
			d, err := decodeOpenRouterDigest(raw, freeFixtureJob(), freeFixture)
			var se *SourceError
			if !errors.As(err, &se) || d.Markdown != "" || se.Diagnostic == "" {
				t.Fatal("reply accepted or diagnostics lost", d, err)
			}
		})
	}
}
func TestOpenRouterDoesNotFollowRedirectOrLeakKey(t *testing.T) {
	hits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer destination.Close()
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		http.Redirect(w, q, destination.URL, http.StatusTemporaryRedirect)
	}, freeFixture)
	_, err := r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"})
	if err == nil || hits != 0 || strings.Contains(err.Error(), "private-test-key") {
		t.Fatal(err, hits)
	}
}
func TestDigestFallbackPolicyPreservesHistoryAndRatings(t *testing.T) {
	s := testStore(t)
	enableDigests(t, s)
	id := add(t, s, "https://example.com/free-fallback")
	manual(t, s, id, "source")
	item, _ := s.Item(id)
	sid := item.Selected.Int64
	if err := s.RateUsefulnessShown(id, 3, &sid, nil, nil, "human"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Ratings(id)
	post := 0
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		post++
		json.NewEncoder(w).Encode(freeFixtureResponse(fixtureDigest))
	}, freeFixture)
	r.Store = s
	primary := generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
		return Digest{}, &SourceError{Kind: "temporary", Message: "primary temporary failure", Retry: true}
	})
	f := &DigestFallback{Primary: primary, Secondary: r, Store: s}
	worker := &Worker{Store: s, Generator: f, Timeout: time.Second, Lease: time.Minute, Backoff: time.Second, MaxAttempts: 3}
	if worked, err := worker.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	d, err := s.DigestForSnapshot(sid)
	if err != nil || d == nil || d.Provider != "openrouter-free" || post != 1 {
		t.Fatal(d, err, post)
	}
	jobs, _ := s.Jobs(id)
	history, err := s.JobFailures(jobs[0].ID)
	if err != nil || len(history) != 1 || history[0].Kind != "temporary" || jobs[0].State != "done" {
		t.Fatal(history, jobs, err)
	}
	after, _ := s.Ratings(id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("human judgment modified")
	}
	for _, kind := range []string{"format", "configuration", "limit", "source", "unknown"} {
		f.Primary = generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
			return Digest{}, &SourceError{Kind: kind, Message: "do not retry", Retry: true}
		})
		if _, err = f.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"}); err == nil {
			t.Fatal("failure hidden")
		}
	}
	if post != 1 {
		t.Fatal("non-temporary failure retried", post)
	}
}
func TestFreeFallbackBudgetAndCancellation(t *testing.T) {
	calls := 0
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		calls++
		if q.Method == "GET" {
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		json.NewEncoder(w).Encode(freeFixtureResponse(fixtureDigest))
	}, freeFixture)
	f := &DigestFallback{Secondary: r, PrimaryBudget: 10 * time.Millisecond, Primary: generatorFunc(func(ctx context.Context, _ Job, _ Snapshot) (Digest, error) {
		<-ctx.Done()
		return Digest{}, &SourceError{Kind: "temporary", Retry: true, Message: "timeout"}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.Generate(ctx, freeFixtureJob(), Snapshot{Text: "source"}); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := f.Generate(cancelled, freeFixtureJob(), Snapshot{Text: "source"}); err == nil || calls != 2 {
		t.Fatal("cancelled task used fallback", err, calls)
	}
}

func TestFreeFallbackPrimarySuccessAndRejectRulesMakeZeroRouterCalls(t *testing.T) {
	routerCalls, primaryCalls := 0, 0
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) { routerCalls++; t.Error("unexpected OpenRouter request") }, freeFixture)
	f := &DigestFallback{Secondary: r, Primary: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
		primaryCalls++
		return Digest{Markdown: fixtureDigest, Provider: "fixture"}, nil
	})}
	if _, err := f.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"}); err != nil || routerCalls != 0 || primaryCalls != 1 {
		t.Fatal(err, routerCalls, primaryCalls)
	}
	s := testStore(t)
	enableDigests(t, s)
	rules, err := NewRuleSet(0.8, `[{"id":"reject","reason":"fixture","field":"url","contains":"/rejected","probability":1}]`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureRules(rules); err != nil {
		t.Fatal(err)
	}
	add(t, s, "https://example.com/rejected")
	worker := &Worker{Store: s, Generator: f, Timeout: time.Second, Lease: time.Minute, MaxAttempts: 3}
	if worked, err := worker.Step(context.Background()); err != nil || worked || routerCalls != 0 || primaryCalls != 1 {
		t.Fatal("rejected item used inference", worked, err, routerCalls, primaryCalls)
	}
}
func TestOpenRouterTemporaryModelFailoverRechecksPricesAndStopsOnFormat(t *testing.T) {
	second := "fixture/beta:free"
	gets, posts := 0, 0
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			gets++
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture, second))
			return
		}
		posts++
		if posts == 1 {
			w.WriteHeader(429)
			return
		}
		response := freeFixtureResponse(fixtureDigest)
		response["model"] = second
		json.NewEncoder(w).Encode(response)
	}, freeFixture, second)
	d, err := r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"})
	if err != nil || d.ActualModel != second || gets != 2 || posts != 2 {
		t.Fatal(d, err, gets, posts)
	}
	posts = 0
	gets = 0
	r.client.Transport = transportFunc(func(q *http.Request) (*http.Response, error) {
		if q.Method == "GET" {
			gets++
			raw, _ := json.Marshal(freeFixtureCatalog(freeFixture, second))
			return &http.Response{StatusCode: 200, Body: ioBody(string(raw))}, nil
		}
		posts++
		return &http.Response{StatusCode: 200, Body: ioBody("broken JSON")}, nil
	})
	if _, err = r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "source"}); err == nil || posts != 1 || gets != 1 {
		t.Fatal("format failure retried", err, posts, gets)
	}
}

func TestOpenRouterProfilePredictionAndBoundedResponse(t *testing.T) {
	profile := "explicit fixture interests"
	content, _ := json.Marshal(map[string]any{"markdown": fixtureDigest, "prediction": map[string]any{"probability": 0.75, "reason": "The fixture profile matches the preserved source."}})
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		var p map[string]any
		json.NewDecoder(q.Body).Decode(&p)
		if p["response_format"].(map[string]any)["type"] != "json_object" {
			t.Fatal("profile route lost JSON mode")
		}
		json.NewEncoder(w).Encode(freeFixtureResponse(string(content)))
	}, freeFixture)
	j := freeFixtureJob()
	j.PromptVersion = DigestPredictionPromptVersion
	j.Interests = profile
	d, err := r.Generate(context.Background(), j, Snapshot{Text: "source"})
	if err != nil || d.Prediction == nil || d.Prediction.Profile != profile || d.Prediction.Probability != 0.75 {
		t.Fatal(d, err)
	}
	r.client.Transport = transportFunc(func(q *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: ioBody(strings.Repeat("x", 17))}, nil
	})
	if _, err = r.request(context.Background(), "GET", "/models", nil, 16); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestFreeFallbackChunkCheckpointsKeepActualRoute(t *testing.T) {
	s := testStore(t)
	if err := s.EnableDigests(DigestConfig{Model: "fixture:cloud", PromptVersion: DigestPromptVersion, MaxInputBytes: 1024, ChunkLimit: 1024, ChunkMaxParts: 64}); err != nil {
		t.Fatal(err)
	}
	id := add(t, s, "https://example.com/free-long")
	source := strings.Repeat("Preserved long source paragraph. ", 100)
	manual(t, s, id, source)
	r := testFreeRouter(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Method == "GET" {
			json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
			return
		}
		json.NewEncoder(w).Encode(freeFixtureResponse(fixtureDigest))
	}, freeFixture)
	r.Store = s
	f := &DigestFallback{Store: s, Secondary: r, Primary: generatorFunc(func(context.Context, Job, Snapshot) (Digest, error) {
		return Digest{}, &SourceError{Kind: "temporary", Retry: true, Message: "fixture unavailable"}
	})}
	w := &Worker{Store: s, Generator: &ChunkGenerator{Store: s, Leaf: f}, Timeout: time.Second, DigestTimeout: 10 * time.Second, Lease: time.Minute, MaxAttempts: 3}
	if worked, err := w.Step(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	item, _ := s.Item(id)
	d, err := s.DigestForSnapshot(item.Selected.Int64)
	if err != nil || d == nil || d.ChunkCount < 2 || d.Provider != "openrouter-free" {
		t.Fatal(d, err)
	}
	var total, bad int
	if err = s.DB.QueryRow("SELECT count(*),sum(CASE WHEN actual_model<>? OR provider<>'openrouter-free' THEN 1 ELSE 0 END) FROM digest_checkpoints", freeFixture).Scan(&total, &bad); err != nil || total < 2 || bad != 0 {
		t.Fatal(total, bad, err)
	}
	snap, err := s.Snapshot(item.Selected.Int64)
	if err != nil || snap.Text != source {
		t.Fatal("source changed", err)
	}
	jobs, _ := s.Jobs(id)
	failures, err := s.JobFailures(jobs[0].ID)
	if err != nil || len(failures) < 2 {
		t.Fatal("per-call failures lost", failures, err)
	}
}

func TestOpenRouterDataPolicyIsExplicitAndDoesNotWidenPrices(t *testing.T) {
	for _, policy := range []string{"deny", "allow"} {
		t.Run(policy, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if q.Method == "GET" {
					json.NewEncoder(w).Encode(freeFixtureCatalog(freeFixture))
					return
				}
				var p map[string]any
				if err := json.NewDecoder(q.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				provider := p["provider"].(map[string]any)
				if provider["data_collection"] != policy || provider["allow_fallbacks"] != false {
					t.Fatal(provider)
				}
				for _, price := range provider["max_price"].(map[string]any) {
					if price != float64(0) {
						t.Fatal("privacy policy widened cost", price)
					}
				}
				json.NewEncoder(w).Encode(freeFixtureResponse(fixtureDigest))
			}))
			defer server.Close()
			r, err := NewFreeOpenRouter("private-test-key", []string{freeFixture}, time.Second, policy)
			if err != nil {
				t.Fatal(err)
			}
			r.baseURL = server.URL
			if _, err = r.Generate(context.Background(), freeFixtureJob(), Snapshot{Text: "Synthetic source"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, policy := range []string{"", "all", "true"} {
		if _, err := NewFreeOpenRouter("private-test-key", []string{freeFixture}, time.Second, policy); err == nil {
			t.Fatal("unknown policy accepted", policy)
		}
	}
	if _, err := NewFreeOpenRouter("private-test-key", []string{freeFixture}, time.Second, "allow", "deny"); err == nil {
		t.Fatal("multiple policies accepted")
	}
}
