// Seed a synthetic store with one material: add the URL, then complete its fetch with
// the given text (no network). For smoke tests against throwaway data directories only.
// usage: go run ./minions/seed-text -data DIR -url URL -text TEXT   (prints the item id)
// The fetch queue of DIR must be empty beforehand: the next due job is the one completed.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/valpere/news-library/internal/library"
)

func main() {
	data := flag.String("data", "", "data directory of the synthetic store")
	url := flag.String("url", "", "material URL")
	text := flag.String("text", "", "source text")
	flag.Parse()
	if *data == "" || *url == "" || *text == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*data, *url, *text); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(data, url, text string) error {
	store, err := library.Open(filepath.Join(data, "library.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	id, err := store.AddURL(url)
	if err != nil {
		return err
	}
	job, err := store.Claim(context.Background(), time.Now(), time.Minute, 3)
	if err != nil {
		return err
	}
	if job == nil || job.ItemID != id {
		return fmt.Errorf("the next due job is not the fetch of item %d; queue must be empty", id)
	}
	if err = store.Complete(*job, library.SourceText{Text: text, Origin: "fetch", Extractor: "seed-text", Completeness: "unknown"}); err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}
