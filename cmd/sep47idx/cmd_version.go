package main

import (
	"flag"
	"fmt"
	"io"
	"runtime"
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
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 {
		errorf(stderr, "sep47idx version: takes no arguments\n")
		return exitError
	}
	if _, err := fmt.Fprintf(stdout, "sep47idx %s (%s, %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH); err != nil {
		return exitError
	}
	return exitOK
}
