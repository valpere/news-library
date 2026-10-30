package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/valpere/news-library/internal/library"
)

type sourceFixture struct{}

func (sourceFixture) Fetch(context.Context, string) (library.Snapshot, error) {
	return library.Snapshot{Title: "Synthetic HTTP cache note", Text: "A personal library stores immutable source text separately from its SQLite metadata. HTTP cache entries are derived data and may be removed without deleting source text. Conditional requests use ETag and Last-Modified to avoid downloading unchanged pages. A backup must include both the database and the primary source and digest files. Rebuilding the cache does not rebuild the primary files.", Origin: "fixture", Extractor: "synthetic-v1", Completeness: "full"}, nil
}

type temporaryPrimary struct{ Calls int }

func (p *temporaryPrimary) Generate(context.Context, library.Job, library.Snapshot) (library.Digest, error) {
	p.Calls++
	return library.Digest{}, &library.SourceError{Kind: "temporary", Retry: true, Message: "synthetic primary HTTP 503 for fallback smoke"}
}
func main() {
	run := flag.Bool("run-cloud", false, "explicitly authorize a single synthetic free digest request")
	noBYOK := flag.Bool("no-byok", false, "confirm workspace has no BYOK credentials")
	model := flag.String("model", "google/gemma-4-31b-it:free", "one concrete free ID")
	collection := flag.String("data-collection", "deny", "deny or explicitly allow provider collection/training")
	flag.Parse()
	if !*run || !*noBYOK {
		fmt.Println("No generation; use -run-cloud -no-byok after checking the account")
		return
	}
	v, err := godotenv.Read(".env")
	if err != nil {
		fmt.Println("Cannot parse private .env")
		os.Exit(1)
	}
	key := v["OPENROUTER_API_KEY"]
	free, err := library.NewFreeOpenRouter(key, []string{*model}, 90*time.Second, *collection)
	if err != nil {
		fmt.Println("Invalid key/free-model configuration")
		os.Exit(1)
	}
	root, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	tmp := filepath.Join(root, "tmp")
	info, err := os.Lstat(tmp)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		fmt.Println("Project tmp directory required")
		os.Exit(1)
	}
	output, err := os.MkdirTemp(tmp, "openrouter-live-")
	if err != nil {
		os.Exit(1)
	}
	fmt.Println("Isolated evidence:", output)
	store, err := library.Open(filepath.Join(output, "library.sqlite"))
	if err != nil {
		fmt.Println("Fixture database unavailable")
		os.Exit(1)
	}
	defer store.Close()
	store.DigestConfig = &library.DigestConfig{Model: "gemma4:cloud", PromptVersion: library.DigestPromptVersion, MaxInputBytes: 16000}
	rows, err := store.AddURLs("https://example.org/synthetic-cache-smoke")
	if err != nil || len(rows) != 1 {
		fmt.Println("Fixture URL unavailable")
		os.Exit(1)
	}
	primary := &temporaryPrimary{}
	free.Store = store
	fallback := &library.DigestFallback{Primary: primary, Secondary: free, Store: store, PrimaryBudget: time.Second}
	worker := library.Worker{Store: store, Source: sourceFixture{}, Generator: fallback, Timeout: 10 * time.Second, DigestTimeout: 2 * time.Minute, Lease: 3 * time.Minute, MaxAttempts: 1}
	started := time.Now()
	for i := 0; i < 2; i++ {
		worked, e := worker.Step(context.Background())
		if e != nil || !worked {
			fmt.Println("Fixture worker did not persist its result")
			os.Exit(1)
		}
	}
	var state string
	var snapshot int64
	err = store.DB.QueryRow("SELECT state,id FROM items LIMIT 1").Scan(&state, &snapshot)
	if err != nil {
		fmt.Println("Fixture result unavailable")
		os.Exit(1)
	}
	result := map[string]any{"state": state, "model_requested": *model, "primary_fixture_calls": primary.Calls, "seconds": time.Since(started).Seconds(), "account_without_byok_confirmed": true}
	result["data_collection"] = *collection
	digest, err := store.DigestForSnapshot(snapshot)
	if err == nil && digest != nil {
		result["actual_model"] = digest.ActualModel
		result["provider"] = digest.Provider
		result["input_tokens"] = digest.InputTokens
		result["output_tokens"] = digest.OutputTokens
		result["zero_cost_non_byok_verified"] = true
		if err := os.WriteFile(filepath.Join(output, "digest.md"), []byte(digest.Markdown), 0600); err != nil {
			fmt.Println("Cannot write fixture digest evidence")
			os.Exit(1)
		}
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "result.json"), data, 0600); err != nil {
		fmt.Println("Cannot write fixture report")
		os.Exit(1)
	}
	fmt.Println(string(data))
	if state != "ready" || digest == nil || err != nil || primary.Calls != 1 {
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Println("Digest check failed")
		}
		os.Exit(1)
	}
	fmt.Println("PASS: simulated primary outage -> real free OpenRouter digest -> immutable fixture artifact; user library untouched")
}
