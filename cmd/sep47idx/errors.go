package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/clierr"
)

// errorBody matches the API error shape (CLAUDE.md §5.11), so one parser
// handles both a failed request and a failed CLI command.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// jsonRequested reports whether this invocation asked for JSON errors. It
// runs before flag parsing so a malformed flag still gets the JSON form, and
// it honours SEP47IDX_JSON for the same reason.
func jsonRequested(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--json="); ok {
			b, err := strconv.ParseBool(v)
			return err == nil && b
		}
		if v, ok := strings.CutPrefix(a, "-json="); ok {
			b, err := strconv.ParseBool(v)
			return err == nil && b
		}
	}
	if v, ok := os.LookupEnv("SEP47IDX_JSON"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		return err == nil && b
	}
	return false
}

// fail writes err in the requested form and returns its catalog exit status.
// JSON goes to stderr because that is where the command's diagnostics belong;
// stdout carries only requested output.
func fail(stderr io.Writer, jsonMode bool, command string, err error) int {
	code, exit := clierr.Classify(err)
	if !jsonMode {
		errorf(stderr, "sep47idx %s: %v\n", command, err)
		return exit
	}
	var body errorBody
	body.Error.Code = code
	body.Error.Message = err.Error()
	// If stderr is unwritable there is nowhere left to report the write
	// failure; the non-zero exit status still signals it.
	_ = json.NewEncoder(stderr).Encode(body)
	return exit
}

// parse treats a flag syntax error as bad input; -h/-help still returns
// flag.ErrHelp so callers can exit 0 after printing usage.
func parse(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		return clierr.Newf("bad_request", "%v", err)
	}
	return err
}

// newFlagSet keeps flag diagnostics out of a JSON error stream. The caller
// still receives the parse error and prints it through fail.
func newFlagSet(name string, stderr io.Writer, jsonMode bool) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if jsonMode {
		fs.SetOutput(io.Discard)
	} else {
		fs.SetOutput(stderr)
	}
	return fs
}
