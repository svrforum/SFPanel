// Command appstore-catalog regenerates appstore/catalog.json from the per-app
// catalog files. Run via `make appstore-catalog` after editing any app.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/svrforum/SFPanel/internal/feature/appstore"
)

func main() {
	root := "appstore"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	data, err := appstore.BuildCatalogBundle(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "appstore-catalog:", err)
		os.Exit(1)
	}
	out := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(out, data, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "appstore-catalog:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, len(data))
}
