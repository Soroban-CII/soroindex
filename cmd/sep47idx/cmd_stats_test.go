package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
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

// tipRPC answers getNetwork and getLatestLedger like a Stellar RPC node.
func tipRPC(t *testing.T, passphrase string, latest uint32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case "getNetwork":
			result = rpc.Network{Passphrase: passphrase}
		case "getLatestLedger":
			result = rpc.LatestLedger{Sequence: latest}
		default:
			t.Errorf("unexpected method %s", req.Method)
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestStatsCLILag(t *testing.T) {
	db, _, _ := readFixture(t) // last indexed ledger 10
	t.Setenv("SEP47IDX_DB", db)

	t.Run("without --lag the snapshot reports no lag and calls no RPC", func(t *testing.T) {
		t.Setenv("SEP47IDX_RPC_URL", "http://127.0.0.1:1") // unreachable: any call would fail
		var out, errb bytes.Buffer
		if code := run([]string{"stats", "--json"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		if strings.Contains(out.String(), `"lag"`) || strings.Contains(out.String(), `"latest_ledger"`) {
			t.Fatalf("lag reported without --lag: %s", out.String())
		}
	})

	t.Run("--lag reports the RPC tip and lag in JSON and text", func(t *testing.T) {
		t.Setenv("SEP47IDX_RPC_URL", tipRPC(t, config.Testnet.Passphrase, 17).URL)
		var out, errb bytes.Buffer
		if code := run([]string{"stats", "--lag", "--json"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		var got struct {
			LastLedger   uint32  `json:"last_ledger"`
			LatestLedger *uint32 `json:"latest_ledger"`
			Lag          *uint32 `json:"lag"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.LastLedger != 10 || got.LatestLedger == nil || *got.LatestLedger != 17 || got.Lag == nil || *got.Lag != 7 {
			t.Fatalf("%v %s", err, out.String())
		}
		out.Reset()
		if code := run([]string{"stats", "--lag"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		if !strings.Contains(out.String(), "Sync: RPC latest ledger 17, lag 7 ledgers") {
			t.Fatal(out.String())
		}
	})

	t.Run("caught up is lag 0, reported rather than omitted", func(t *testing.T) {
		t.Setenv("SEP47IDX_RPC_URL", tipRPC(t, config.Testnet.Passphrase, 10).URL)
		var out, errb bytes.Buffer
		if code := run([]string{"stats", "--lag", "--json"}, &out, &errb); code != 0 {
			t.Fatal(code, errb.String())
		}
		if !strings.Contains(out.String(), `"lag": 0`) && !strings.Contains(out.String(), `"lag":0`) {
			t.Fatal(out.String())
		}
	})

	t.Run("an RPC on another network is refused", func(t *testing.T) {
		t.Setenv("SEP47IDX_RPC_URL", tipRPC(t, "Public Global Stellar Network ; September 2015", 17).URL)
		var errb bytes.Buffer
		if code := run([]string{"stats", "--lag"}, &bytes.Buffer{}, &errb); code != 1 || !strings.Contains(errb.String(), "does not match testnet") {
			t.Fatal(code, errb.String())
		}
	})

	t.Run("an RPC behind the index is an error, not a negative lag", func(t *testing.T) {
		t.Setenv("SEP47IDX_RPC_URL", tipRPC(t, config.Testnet.Passphrase, 9).URL)
		var errb bytes.Buffer
		if code := run([]string{"stats", "--lag"}, &bytes.Buffer{}, &errb); code != 1 || !strings.Contains(errb.String(), "ahead of RPC latest 9") {
			t.Fatal(code, errb.String())
		}
	})
}
