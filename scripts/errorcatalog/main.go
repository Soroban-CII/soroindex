// Command errorcatalog writes docs/errors.md from internal/clierr's
// catalog. Run it through scripts/gen-error-catalog.sh; a test fails when the
// committed page differs from the catalog.
package main

import (
	"fmt"
	"os"

	"github.com/Soroban-CII/soroindex/internal/clierr"
)

func main() {
	if _, err := os.Stdout.WriteString(clierr.Markdown()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
