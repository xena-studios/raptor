// Command eggexport writes the egg library (library/, docs/EGGS.md#egg-library)
// as files to import into another Panel's egg library: one per egg, with its
// icon and Raptor's settings in it. Raptor's own Panel doesn't need them: it
// seeds the library on every start. Run it with task library:export.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xena-studios/raptor/library"
)

func main() {
	out := flag.String("out", "library-export", "the folder to write the eggs to")
	flag.Parse()
	all, err := library.All()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil { //nolint:gosec // a folder for files to import
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, e := range all {
		file, err := e.ImportFile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", e.ID, err)
			os.Exit(1)
		}
		name := strings.ReplaceAll(e.ID, "/", "-") + ".json"
		if err := os.WriteFile(filepath.Join(*out, name), file, 0o644); err != nil { //nolint:gosec // files to import
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d eggs to %s\n", len(all), *out)
}
