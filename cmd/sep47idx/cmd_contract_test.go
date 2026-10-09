package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func TestContractCLIPositionalFlagsAndNotFound(t *testing.T) {
	db, id, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	for _, args := range [][]string{{"contract", id, "--json"}, {"contract", "--json", id}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		var d store.ContractDetail
		if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.ContractID != id || !d.Claims[0].Declared {
			t.Fatal(out.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"contract", id, "--history", "--json"}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var h store.HistoryDetail
	if err := json.Unmarshal(out.Bytes(), &h); err != nil || len(h.History) != 1 || h.History[0].FromLedger != 10 {
		t.Fatal(out.String())
	}
	b := make([]byte, 32)
	b[0] = 1
	missing, err := strkey.Encode(strkey.VersionByteContract, b)
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"contract", missing}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal(code)
	}
	for _, args := range [][]string{{"contract", "invalid"}, {"contract"}, {"contract", id, "extra"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}
