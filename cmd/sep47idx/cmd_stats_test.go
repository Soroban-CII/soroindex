package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestStatsCLIStoredSnapshot(t *testing.T) {
	db, _, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"stats", "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d store.Statistics
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.LastLedger != 10 || d.TotalContracts != 1 || d.Adoption.DeclaringContracts != 1 || d.RulesetVersions[41] == "" {
		t.Fatal(out.String())
	}
	if code := run([]string{"stats", "extra"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatal(code)
	}
	if code := run([]string{"stats"}, failWriter{}, &bytes.Buffer{}); code != 1 {
		t.Fatal(code)
	}
}
