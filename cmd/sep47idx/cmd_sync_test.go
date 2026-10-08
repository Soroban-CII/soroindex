package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func syncRPC(t *testing.T, oldest uint32) *httptest.Server {
	t.Helper()
	meta := xdr.LedgerCloseMeta{V: 0, V0: &xdr.LedgerCloseMetaV0{LedgerHeader: xdr.LedgerHeaderHistoryEntry{Header: xdr.LedgerHeader{LedgerSeq: 10}}}}
	encoded, err := xdr.MarshalBase64(meta)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		var result any
		switch req.Method {
		case "getNetwork":
			result = rpc.Network{Passphrase: config.Testnet.Passphrase}
		case "getLatestLedger":
			result = rpc.LatestLedger{Sequence: 10}
		case "getLedgers":
			result = rpc.LedgersPage{OldestLedger: oldest, LatestLedger: 10, Ledgers: []rpc.Ledger{{Sequence: 10, MetadataXDR: encoded}}}
		default:
			t.Errorf("unexpected method %s", req.Method)
			http.Error(w, "unknown method", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  any             `json:"result"`
		}{"2.0", req.ID, result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
func TestSyncCLIStartsAndResumes(t *testing.T) {
	server := syncRPC(t, 1)
	db := filepath.Join(t.TempDir(), "testnet.db")
	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		args := []string{"sync", "--db", db, "--rpc-url", server.URL}
		if i == 0 {
			args = append(args, "--start-ledger", "10")
		}
		if code := run(args, &out, &errb); code != exitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(errb.String(), `"caught_up":true`) {
			t.Fatalf("missing caught_up: %s", errb.String())
		}
	}
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "10" {
		t.Fatalf("last %q: %v", last, err)
	}
}
func TestSyncCLIRetentionGapLeavesResumeState(t *testing.T) {
	server := syncRPC(t, 10)
	db := filepath.Join(t.TempDir(), "testnet.db")
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(context.Background(), store.KeyLastLedger, "8"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"sync", "--db", db, "--rpc-url", server.URL}, &out, &errb); code != exitError || !strings.Contains(errb.String(), "seed or backfill") {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	s, err = store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "8" {
		t.Fatalf("last %q: %v", last, err)
	}
	if _, err := s.State(context.Background(), store.KeyRPCURL); err == nil {
		t.Fatal("retention gap wrote rpc_url")
	}
}
func TestSyncCLIRejectsInvalidFlagsBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{{"--start-ledger", "0"}, {"--start-ledger", "4294967296"}, {"--interval", "0s"}, {"extra"}} {
		var out, errb bytes.Buffer
		if code := runSync(args, &out, &errb); code != exitError {
			t.Fatalf("%v accepted", args)
		}
	}
}
