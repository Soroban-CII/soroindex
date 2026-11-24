package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/Soroban-CII/soroindex/internal/clierr"
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
	jsonMode := jsonRequested(args)
	fs := newFlagSet("version", stderr, jsonMode)
	if err := parse(fs, args); err != nil {
		return readExit("version", err, stderr, jsonMode)
	}
	if fs.NArg() != 0 {
		return fail(stderr, jsonMode, "version", clierr.Newf("bad_request", "takes no arguments"))
	}
	if _, err := fmt.Fprintf(stdout, "sep47idx %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH); err != nil {
		return fail(stderr, jsonMode, "version", err)
	}
	return exitOK
}
