package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decodeError(t *testing.T, stderr string) errorBody {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSON error should be one line, got %d:\n%s", len(lines), stderr)
	}
	var body errorBody
	if err := json.Unmarshal([]byte(lines[0]), &body); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("error shape incomplete: %s", stderr)
	}
	return body
}

func TestJSONErrorFormAndCodes(t *testing.T) {
	t.Setenv("SEP47IDX_DB", "")
	tests := []struct {
		name string
		args []string
		code string
		exit int
	}{
		{"unknown command", []string{"nope", "--json"}, "bad_request", 1},
		{"unknown flag", []string{"gap", "--bogus", "--json"}, "bad_request", 1},
		{"invalid SEP", []string{"gap", "--sep", "0", "--json"}, "bad_request", 1},
		{"missing rule file", []string{"gap", "--sep", "50", "--json"}, "not_found", 2},
		{"invalid query", []string{"query", "--limit", "0", "--json"}, "bad_request", 1},
		{"stats positional argument", []string{"stats", "extra", "--json"}, "bad_request", 1},
		{"version positional argument", []string{"version", "extra", "--json"}, "bad_request", 1},
		{"maintenance positional argument", []string{"maintenance", "extra", "--json"}, "bad_request", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := run(tt.args, &out, &stderr)
			if code != tt.exit {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tt.exit, stderr.String())
			}
			if out.Len() != 0 {
				t.Errorf("error run wrote stdout: %q", out.String())
			}
			body := decodeError(t, stderr.String())
			if body.Error.Code != tt.code {
				t.Errorf("code = %q, want %q (%s)", body.Error.Code, tt.code, body.Error.Message)
			}
		})
	}
}

func TestJSONErrorFromEnvironmentAndTextStaysPlain(t *testing.T) {
	t.Setenv("SEP47IDX_JSON", "true")
	var out, stderr bytes.Buffer
	if code := run([]string{"gap", "--sep", "0"}, &out, &stderr); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if got := decodeError(t, stderr.String()); got.Error.Code != "bad_request" {
		t.Fatalf("env JSON not honoured: %+v", got)
	}

	t.Setenv("SEP47IDX_JSON", "")
	stderr.Reset()
	if code := run([]string{"gap", "--sep", "0"}, &out, &stderr); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasPrefix(stderr.String(), "sep47idx gap: ") || strings.Contains(stderr.String(), "\"error\"") {
		t.Fatalf("text diagnostics changed: %q", stderr.String())
	}
}

func TestJSONErrorMapsCodedErrors(t *testing.T) {
	// A missing database is an operator error; the stored-data sentinels keep
	// their codes when they surface through a command.
	t.Setenv("SEP47IDX_DB", t.TempDir()+"/missing.db")
	var out, stderr bytes.Buffer
	code := run([]string{"contract", "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4", "--json"}, &out, &stderr)
	if code != 1 && code != 2 {
		t.Fatalf("exit = %d", code)
	}
	body := decodeError(t, stderr.String())
	switch body.Error.Code {
	case "database_uninitialized", "bad_request", "not_found", "internal":
	default:
		t.Fatalf("unexpected code for a missing database: %q (%s)", body.Error.Code, body.Error.Message)
	}
}
