package main

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args prints usage to stderr and fails", nil, exitError, "", "usage: sep47idx"},
		{"help prints usage to stdout and succeeds", []string{"help"}, exitOK, "usage: sep47idx", ""},
		{"--help is an alias for help", []string{"--help"}, exitOK, "usage: sep47idx", ""},
		{"unknown command fails and names it", []string{"nope"}, exitError, "", `unknown command "nope"`},
		{"version prints version and toolchain", []string{"version"}, exitOK, "sep47idx dev (" + runtime.Version(), ""},
		{"version rejects positional args", []string{"version", "extra"}, exitError, "", "takes no arguments"},
		{"version rejects unknown flags", []string{"version", "-x"}, exitError, "", "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantStdout != "" && !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", out.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", errb.String(), tt.wantStderr)
			}
		})
	}
}

func TestUsageListsVersion(t *testing.T) {
	if u := usage(); !strings.Contains(u, "version") {
		t.Fatalf("usage does not list the version command:\n%s", u)
	}
}

func TestAllBuiltCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"phase0", "sync", "serve", "query", "contract", "wasm", "stats", "gap", "version"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("command %s missing on %s/%s", name, runtime.GOOS, runtime.GOARCH)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestStdoutWriteFailureIsAnError(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"help"}} {
		t.Run(args[0]+" fails when stdout is unwritable", func(t *testing.T) {
			if code := run(args, failWriter{}, &bytes.Buffer{}); code != exitError {
				t.Fatalf("exit code = %d, want %d", code, exitError)
			}
		})
	}
}
