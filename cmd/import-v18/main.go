// Command import-v18 is a one-off tool that moves a schema-18 data directory
// into the current model. Delete it once the live data has been moved.
package main

import (
	"fmt"
	"os"

	"github.com/valpere/news-library/internal/library"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: import-v18 <old-data-dir> <new-data-dir>\nThe old directory is only read (stop the service or pass a copy: a live WAL database may lag); the new one must not exist or be empty.")
		os.Exit(2)
	}
	r, err := library.ImportV18(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "import failed:", err)
		os.Exit(1)
	}
	fmt.Printf("%+v\nnot carried over (recalculated by the queue): knowledge, embeddings, connection notes, cache, chat\n", r)
}
