package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func init() {
	commands["version"] = command{
		summary: "print the binary version",
		run:     runVersion,
	}
}

// runVersion prints the build version and the Go toolchain that built it,
// so bug reports carry enough to reproduce a build.
func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Bool("json", false, "print version as JSON")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		errorf(stderr, "sep47idx version: takes no arguments\n")
		return exitError
	}
	if f := fs.Lookup("json"); f != nil && f.Value.String() == "true" {
		data := map[string]string{
			"version":    version,
			"go_version": runtime.Version(),
			"os_arch":    runtime.GOOS + "/" + runtime.GOARCH,
			"parser":     sepmeta.ParserVersion,
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(data); err != nil {
			return exitError
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(stdout, "sep47idx %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH); err != nil {
		return exitError
	}
	return exitOK
}
