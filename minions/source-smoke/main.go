// Explicit read-only live source smoke: no database, models, or source writes.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/valpere/news-library/internal/library"
	"os"
	"time"
)

func main() {
	raw := flag.String("fetch-url", "", "explicit URL to fetch live")
	flag.Parse()
	if *raw == "" {
		fmt.Fprintln(os.Stderr, "use -fetch-url URL")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := library.NewFetcher(30*time.Second, 2<<20)
	s, err := f.Fetch(ctx, *raw)
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Printf("PASS extractor=%s completeness=%s text_bytes=%d warning=%s\n", s.Extractor, s.Completeness, len(s.Text), s.Warning)
}
