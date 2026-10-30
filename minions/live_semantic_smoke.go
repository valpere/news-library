// Explicit local bge-m3 integration smoke; fixtures stay under ./tmp/.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/valpere/news-library/internal/library"
)

func run() error {
	enabled := flag.Bool("run-local-embeddings", false, "explicitly run local bge-m3 requests")
	endpoint := flag.String("embed-url", "http://127.0.0.1:11434/api/embed", "local Ollama embedding endpoint")
	flag.Parse()
	if !*enabled {
		return fmt.Errorf("pass -run-local-embeddings; excluded from routine checks")
	}
	if err := os.MkdirAll("tmp", 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("tmp", "semantic-live-")
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	store, err := library.Open(filepath.Join(dir, "library.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	bge, err := library.NewBGE(*endpoint, 60*time.Second)
	if err != nil {
		return err
	}
	semantic := &library.Semantic{Store: store, Embedder: bge, ChunkBytes: 4096, Dimensions: 1024, Timeout: 60 * time.Second}
	fixtures := []string{
		"SQLite write-ahead logging and transactions preserve committed database records after a crash or power failure. Restore a consistent database backup and its archived text files to recover the application after restarting.",
		"A recipe for sourdough bread: combine flour and water, knead the dough, let it ferment overnight, and bake until the crust is golden brown.",
		"Astronomers observe distant galaxies and stellar spectra to study the formation of stars and the expansion of the universe.",
	}
	for n, text := range fixtures {
		id, e := store.AddURL(fmt.Sprintf("https://example.com/semantic-fixture-%d", n+1))
		if e != nil {
			return e
		}
		if e = store.Manual(id, text); e != nil {
			return e
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for n := 0; n < 10; n++ {
		worked, e := semantic.Step(ctx)
		if e != nil {
			return e
		}
		if !worked {
			break
		}
	}
	result, err := semantic.Search(ctx, library.Filters{Query: "Як зберегти дані при аварійному перезапуску і потім відновити їх?"}, "vector")
	if err != nil {
		return err
	}
	if result.Mode != "vector" || len(result.Items) == 0 || result.Items[0].ID != 1 {
		return fmt.Errorf("semantic fixture did not rank database recovery first")
	}
	item, err := store.Item(1)
	if err != nil {
		return err
	}
	if item.Read || item.Score.Valid {
		return fmt.Errorf("human state changed")
	}
	revision, err := bge.Revision(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"model": "bge-m3:latest", "revision": revision, "fixture": dir, "mode": result.Mode, "indexed": result.Indexed, "first_id": result.Items[0].ID, "first_snippet": result.Items[0].MatchSnippet, "human_state_unchanged": true})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "semantic-smoke:", err)
		os.Exit(1)
	}
}
