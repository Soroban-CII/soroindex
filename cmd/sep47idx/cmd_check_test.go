package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestCheckCLIReportsMatchingIndex(t *testing.T) {
	db, id, hash := readFixture(t)
	srv := newCheckRPC(t, id, hash)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"check", "--sample", "1", "--seed", "1"}, &out, &errb); code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "checked 1 of 1") || !strings.Contains(out.String(), "no differences") {
		t.Fatalf("output = %s", out.String())
	}

	out.Reset()
	if code := run([]string{"check", "--sample", "1", "--seed", "1", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("json code = %d, stderr = %s", code, errb.String())
	}
	var res ingest.CheckResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Checked != 1 || len(res.Differences) != 0 {
		t.Fatalf("json = %s (%v)", out.String(), err)
	}
}

func TestCheckCLIExitsOneOnDifference(t *testing.T) {
	db, id, _ := readFixture(t)
	other := strings.Repeat("b", 64)
	srv := newCheckRPC(t, id, other)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"check", "--sample", "1"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, output = %s, stderr = %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "wasm_hash") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestCheckCLIRejectsBadInput(t *testing.T) {
	db, _, _ := readFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	for _, args := range [][]string{
		{"check", "--sample", "0"},
		{"check", "--sample", "100001"},
		{"check", "extra"},
	} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != exitError {
			t.Fatalf("%v: code = %d", args, code)
		}
	}
}

// newCheckRPC serves the three methods check calls. The instance entry it
// returns carries hash, which need not match the database's.
func newCheckRPC(t *testing.T, id, hash string) *httptest.Server {
	t.Helper()
	entry := checkInstanceXDR(t, id, hash)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "getNetwork":
			result = map[string]any{"passphrase": config.Testnet.Passphrase, "protocolVersion": 29}
		case "getLatestLedger":
			result = map[string]any{"sequence": 12}
		case "getLedgerEntries":
			result = map[string]any{"latestLedger": 12, "entries": []map[string]any{{
				"key": checkInstanceKey(t, id), "xdr": entry, "lastModifiedLedgerSeq": 10,
			}}}
		default:
			t.Errorf("unexpected method %s", req.Method)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checkInstanceXDR(t *testing.T, id, hash string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hex.DecodeString(hash)
	if err != nil || len(h) != 32 {
		t.Fatalf("hash %q: %v", hash, err)
	}
	var xh xdr.Hash
	copy(xh[:], h)
	cid := xdr.ContractId(raw)
	inst := xdr.ScContractInstance{Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &xh}}
	cd := xdr.ContractDataEntry{
		Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid},
		Durability: xdr.ContractDataDurabilityPersistent,
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Val:        xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &inst},
	}
	s, err := xdr.MarshalBase64(xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func checkInstanceKey(t *testing.T, id string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil {
		t.Fatal(err)
	}
	cid := xdr.ContractId(raw)
	s, err := xdr.MarshalBase64(xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid},
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Durability: xdr.ContractDataDurabilityPersistent,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
