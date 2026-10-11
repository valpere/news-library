// Offline, read-only digest validation; no generation or library writes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/valpere/news-library/internal/library"
	"os"
)

func main() {
	file := flag.String("file", "", "Markdown file or saved Ollama JSON response")
	envelope := flag.Bool("ollama-response", false, "read message.content from an Ollama JSON envelope")
	flag.Parse()
	if *file == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "use -file PATH [-ollama-response]")
		os.Exit(2)
	}
	info, err := os.Stat(*file)
	if err != nil || info.Size() > 2<<20 {
		fmt.Fprintln(os.Stderr, "input unavailable or larger than 2 MiB")
		os.Exit(1)
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	text := string(data)
	if *envelope {
		var r struct {
			Done    bool `json:"done"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &r) != nil || !r.Done || r.Message.Content == "" {
			fmt.Fprintln(os.Stderr, "invalid/incomplete Ollama response")
			os.Exit(1)
		}
		text = r.Message.Content
	}
	sections, err := library.SplitDigest(text)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("PASS: %d digest sections; no generation or writes\n", len(sections))
}
