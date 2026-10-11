// Read-only URL list audit using the application's canonicalizer; no fetch/import.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/valpere/news-library/internal/library"
	"os"
	"strings"
	"unicode/utf8"
)

func main() {
	file := flag.String("file", "", "UTF-8 file containing one URL per line")
	flag.Parse()
	if *file == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "use -file PATH")
		os.Exit(2)
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !utf8.Valid(data) {
		fmt.Fprintln(os.Stderr, "input is not UTF-8")
		os.Exit(1)
	}
	type failure struct {
		Line   int
		Reason string
	}
	type duplicate struct{ Line, FirstLine int }
	invalid := []failure{}
	duplicates := []duplicate{}
	seen := map[string]int{}
	nonempty := 0
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonempty++
		canonical, e := library.CanonicalURL(line)
		if e != nil {
			invalid = append(invalid, failure{i + 1, e.Error()})
			continue
		}
		if first, ok := seen[canonical]; ok {
			duplicates = append(duplicates, duplicate{i + 1, first})
		} else {
			seen[canonical] = i + 1
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"nonempty_lines": nonempty, "unique_canonical_urls": len(seen), "invalid": invalid, "duplicates": duplicates, "fits_batch": len(data) <= library.BatchInputLimit && nonempty > 0 && nonempty <= library.BatchURLLimit})
	if len(invalid) > 0 {
		os.Exit(1)
	}
}
