package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var decimalZero = regexp.MustCompile(`^-?0+(\.0+)?([eE][+-]?[0-9]{1,4})?$`)

var freeModelID = regexp.MustCompile(`^[a-zA-Z0-9_-]+/[a-zA-Z0-9][a-zA-Z0-9._-]*:free$`)

func ParseFreeModels(raw string) ([]string, error) {
	var models []string
	if !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return nil, errors.New("OpenRouter models must be a JSON array")
	}
	if err := json.Unmarshal([]byte(raw), &models); err != nil || len(models) > 3 {
		return nil, errors.New("OpenRouter models must be a JSON array of up to three concrete free IDs")
	}
	seen := map[string]bool{}
	for _, m := range models {
		if len(m) > 256 || !freeModelID.MatchString(m) || strings.HasPrefix(strings.ToLower(m), "openrouter/") || seen[m] {
			return nil, errors.New("OpenRouter requires unique concrete provider/model:free IDs")
		}
		seen[m] = true
	}
	return models, nil
}

type FreeOpenRouter struct {
	key            string
	models         []string
	baseURL        string
	client         *http.Client
	Store          *Store
	dataCollection string
}

func NewFreeOpenRouter(key string, models []string, timeout time.Duration, collection ...string) (*FreeOpenRouter, error) {
	policy := "deny"
	if len(collection) > 1 {
		return nil, errors.New("one OpenRouter data policy required")
	}
	if len(collection) == 1 {
		policy = collection[0]
	}
	if policy != "deny" && policy != "allow" {
		return nil, errors.New("OpenRouter data collection must be deny or allow")
	}
	raw, _ := json.Marshal(models)
	validated, err := ParseFreeModels(string(raw))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") || len(validated) == 0 || timeout <= 0 {
		return nil, errors.New("enabled OpenRouter requires a private key, explicit free IDs and a positive timeout")
	}
	return &FreeOpenRouter{key: key, models: append([]string(nil), validated...), dataCollection: policy, baseURL: "https://openrouter.ai/api/v1", client: &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func zeroPrice(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	return len(value) <= 128 && decimalZero.MatchString(value)
}
func (or *FreeOpenRouter) request(ctx context.Context, method, path string, payload []byte, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, or.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if method == "POST" {
		req.Header.Set("Authorization", "Bearer "+or.key)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := or.client.Do(req)
	if err != nil {
		return nil, &SourceError{Kind: "temporary", Message: "OpenRouter недоступний або перевищено час очікування", Retry: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		kind := "configuration"
		retry := resp.StatusCode == 429 || resp.StatusCode >= 500
		if retry {
			kind = "temporary"
		}
		return nil, &SourceError{Kind: kind, Message: fmt.Sprintf("OpenRouter HTTP %d", resp.StatusCode), Retry: retry}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &SourceError{Kind: "temporary", Message: "відповідь OpenRouter перервано", Retry: true}
	}
	if int64(len(body)) > limit {
		return nil, &SourceError{Kind: "limit", Message: "відповідь OpenRouter перевищує ліміт"}
	}
	return body, nil
}
func (or *FreeOpenRouter) checkPrices(ctx context.Context) error {
	body, err := or.request(ctx, "GET", "/models", nil, 8<<20)
	if err != nil {
		return err
	}
	var catalog struct {
		Data []struct {
			ID      string                     `json:"id"`
			Pricing map[string]json.RawMessage `json:"pricing"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &catalog); err != nil {
		return &SourceError{Kind: "configuration", Message: "не вдалося перевірити ціни OpenRouter"}
	}
	for _, m := range or.models {
		found := false
		for _, entry := range catalog.Data {
			if entry.ID != m {
				continue
			}
			if found {
				return &SourceError{Kind: "configuration", Message: "неоднозначний каталог OpenRouter"}
			}
			found = true
			if !zeroPrice(entry.Pricing["prompt"]) || !zeroPrice(entry.Pricing["completion"]) {
				return &SourceError{Kind: "configuration", Message: "резерв OpenRouter не підтвердив нульову ціну моделі"}
			}
			for _, price := range entry.Pricing {
				if !zeroPrice(price) {
					return &SourceError{Kind: "configuration", Message: "резерв OpenRouter містить невідому або ненульову ціну"}
				}
			}
		}
		if !found {
			return &SourceError{Kind: "configuration", Message: "явно дозволена free-модель відсутня у каталозі OpenRouter"}
		}
	}
	return nil
}
func (or *FreeOpenRouter) Generate(ctx context.Context, j Job, snap Snapshot) (Digest, error) {
	messages, err := digestMessages(j, snap)
	if err != nil {
		return Digest{}, err
	}
	if !cloudModelID(j.Model) {
		return Digest{}, &SourceError{Kind: "configuration", Message: "резерв потребує дозволену первинну cloud-модель"}
	}
	var last error
	for i, m := range or.models {
		if ctx.Err() != nil {
			return Digest{}, ctx.Err()
		}
		if err = or.checkPrices(ctx); err != nil {
			return Digest{}, err
		}
		payload := map[string]any{"model": m, "messages": messages, "stream": false, "max_tokens": 4096, "temperature": 0.3, "provider": map[string]any{"max_price": map[string]int{"prompt": 0, "completion": 0, "request": 0, "image": 0}, "require_parameters": true, "allow_fallbacks": false, "data_collection": or.dataCollection}}
		if j.PromptVersion == DigestPredictionPromptVersion {
			payload["response_format"] = map[string]string{"type": "json_object"}
		}
		raw, _ := json.Marshal(payload)
		body, e := or.request(ctx, "POST", "/chat/completions", raw, 2<<20)
		if e == nil {
			return decodeOpenRouterDigest(body, j, m)
		}
		e = fmt.Errorf("OpenRouter %s: %w", m, e)
		last = e
		if !temporaryGenerationFailure(e) || i+1 == len(or.models) {
			return Digest{}, e
		}
		if or.Store != nil {
			if e = or.Store.recordAttemptFailure(j, e); e != nil {
				return Digest{}, e
			}
		}
	}
	return Digest{}, last
}
func decodeOpenRouterDigest(body []byte, j Job, requested string) (Digest, error) {
	var r struct {
		Model   string          `json:"model"`
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index   int    `json:"index"`
			Finish  string `json:"finish_reason"`
			Message struct {
				Role, Content string
				Refusal       json.RawMessage `json:"refusal"`
				ToolCalls     json.RawMessage `json:"tool_calls"`
				FunctionCall  json.RawMessage `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Cost   json.RawMessage `json:"cost"`
			Input  int64           `json:"prompt_tokens"`
			Output int64           `json:"completion_tokens"`
			BYOK   *bool           `json:"is_byok"`
		} `json:"usage"`
	}
	invalid := func(message string, kind string) (Digest, error) {
		return Digest{}, &SourceError{Kind: kind, Message: message, Diagnostic: string(body)}
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return invalid("неправильна відповідь OpenRouter", "format")
	}
	if (r.Model != requested && r.Model != strings.TrimSuffix(requested, ":free")) || !zeroPrice(r.Usage.Cost) || r.Usage.BYOK == nil || *r.Usage.BYOK {
		return invalid("OpenRouter не підтвердив дозволену модель і нульову вартість", "configuration")
	}
	if len(r.Error) > 0 && string(r.Error) != "null" {
		return invalid("OpenRouter повернув помилку всередині відповіді", "format")
	}
	if len(r.Choices) != 1 || r.Choices[0].Index != 0 || r.Choices[0].Finish != "stop" || r.Choices[0].Message.Role != "assistant" || strings.TrimSpace(r.Choices[0].Message.Content) == "" || r.Usage.Input < 0 || r.Usage.Output < 0 {
		return invalid("незавершена або некоректна відповідь OpenRouter", "format")
	}
	msg := r.Choices[0].Message
	if (len(msg.Refusal) > 0 && string(msg.Refusal) != "null") || (len(msg.ToolCalls) > 0 && string(msg.ToolCalls) != "null" && string(msg.ToolCalls) != "[]") || (len(msg.FunctionCall) > 0 && string(msg.FunctionCall) != "null") {
		return invalid("OpenRouter повернув відмову або недозволені tools", "format")
	}
	d, err := decodeDigestContent(msg.Content, j)
	if err != nil {
		return Digest{}, err
	}
	d.ActualModel = requested
	d.Provider = "openrouter-free"
	d.InputTokens = r.Usage.Input
	d.OutputTokens = r.Usage.Output
	return d, nil
}
func temporaryGenerationFailure(err error) bool {
	var source *SourceError
	return errors.As(err, &source) && source.Kind == "temporary" && source.Retry
}

type DigestFallback struct {
	Primary       Generator
	Secondary     *FreeOpenRouter
	Store         *Store
	PrimaryBudget time.Duration
}

func (f *DigestFallback) Generate(ctx context.Context, j Job, snap Snapshot) (Digest, error) {
	if _, err := digestMessages(j, snap); err != nil {
		return Digest{}, err
	}
	if f.Secondary == nil {
		return f.Primary.Generate(ctx, j, snap)
	}
	primaryCtx := ctx
	cancel := func() {}
	budget := f.PrimaryBudget
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline) / 2
		if budget <= 0 || left < budget {
			budget = left
		}
	}
	if budget > 0 {
		primaryCtx, cancel = context.WithTimeout(ctx, budget)
	}
	d, err := f.Primary.Generate(primaryCtx, j, snap)
	cancel()
	if err == nil || !temporaryGenerationFailure(err) || ctx.Err() != nil {
		return d, err
	}
	if f.Store != nil {
		if e := f.Store.recordAttemptFailure(j, err); e != nil {
			return Digest{}, e
		}
	}
	return f.Secondary.Generate(ctx, j, snap)
}
func (s *Store) recordAttemptFailure(j Job, err error) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = leaseValid(tx, j); e != nil {
		return e
	}
	replaced, e := s.recordFailure(tx, j, FailureFromError(err))
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.removeUnusedFiles(map[string]bool{replaced: replaced != ""}) // best effort
	return nil
}
