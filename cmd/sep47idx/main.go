// Command sep47idx indexes Soroban contracts by the SEPs they declare,
// the interfaces they match, and (on testnet) the suites they pass.
//
// main.go only dispatches to subcommands; each subcommand lives in its
// own cmd_<name>.go file so that the CLI surface stays reviewable.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Exit codes are part of the CLI contract (CLAUDE.md §5.12) so scripts
// can tell "not found" apart from a real failure.
const (
	exitOK       = 0
	exitError    = 1
	exitNotFound = 2
)

// version is overwritten at link time with -X main.version=... so that
// a released binary reports its tag; "dev" marks a local build.
var version = "dev"

// command is one subcommand. run receives the arguments after the
// subcommand name and returns the process exit code.
type command struct {
	summary string
	run     func(args []string, stdout, stderr io.Writer) int
}

// commands is the dispatch table. Each cmd_*.go file adds its entry in
// an init function, which keeps main.go free of per-command logic.
var commands = map[string]command{}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without os.Exit, so tests can drive the dispatcher.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		errorf(stderr, "%s", usage())
		return exitError
	}
	name := args[0]
	if name == "-h" || name == "--help" || name == "help" {
		if _, err := io.WriteString(stdout, usage()); err != nil {
			return exitError
		}
		return exitOK
	}
	cmd, ok := commands[name]
	if !ok {
		errorf(stderr, "sep47idx: unknown command %q\n\n%s", name, usage())
		return exitError
	}
	return cmd.run(args[1:], stdout, stderr)
}

// usage renders the command list in name order.
func usage() string {
	var b strings.Builder
	b.WriteString("usage: sep47idx <command> [flags]\n\ncommands:\n")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "  %-10s %s\n", n, commands[n].summary)
	}
	return b.String()
}

// errorf writes a diagnostic to stderr. The write error is discarded on
// purpose: if stderr itself is unwritable there is nowhere left to report
// it, and the caller's non-zero exit code still signals the failure.
func errorf(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format, args...)
}
