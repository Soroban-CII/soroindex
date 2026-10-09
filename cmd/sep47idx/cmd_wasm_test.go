package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestWasmCLIJSONAndValidation(t *testing.T) {
	db, _, hash := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"wasm", hash, "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var d store.WasmDetail
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || len(d.Claims) != 1 || d.Hash != hash {
		t.Fatal(out.String())
	}
	if code := run([]string{"wasm", strings.Repeat("b", 64)}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal(code)
	}
	for _, args := range [][]string{{"wasm", strings.ToUpper(hash)}, {"wasm", "oops"}, {"wasm", hash, "--limit", "0"}, {"wasm", hash, "--cursor", "bad"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}
