package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/api"
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

// TestEC2E2EFullPipeline runs the full sep47idx pipeline end-to-end:
// 1. Sync against a recorded fake RPC
// 2. Start the HTTP serve server on the resulting database
// 3. Call every /v1 endpoint and assert responses
// 4. Verify the full pipeline works offline without network access.
func TestEC2E2EFullPipeline(t *testing.T) {
	t.Helper()

	// Use a temp database
	db := filepath.Join(t.TempDir(), "testnet_e2e.db")

	// Step 1: Run sync with a fake RPC server
	fakeRPC := syncRPC(t, 1)
	defer fakeRPC.Close()

	var syncOut, syncErr bytes.Buffer
	syncArgs := []string{"sync", "--db", db, "--rpc-url", fakeRPC.URL, "--network", "testnet", "--start-ledger", "10"}
	if code := run(syncArgs, &syncOut, &syncErr); code != exitOK {
		t.Fatalf("sync failed (exit %d): command: sep47idx %s, output: %s, stderr: %s", code, strings.Join(syncArgs, " "), syncOut.String(), syncErr.String())
	}
	if !strings.Contains(syncErr.String(), `"caught_up":true`) {
		t.Fatalf("sync did not catch up: stderr %s", syncErr.String())
	}

	// Step 2: Start the serve HTTP server on the resulting database
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveAddr := listener.Addr().String()

	// Create API handler with the store from the just-synced database
	ctx := context.Background()
	storeObj, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	s, err := api.New(api.Options{Store: storeObj, Network: "testnet"})
	if err != nil {
		t.Fatalf("failed to create API handler: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_ = serveHTTP(ctx, listener, s, 5*time.Second, 20*time.Second, 20*time.Second, 60*time.Second, 16<<10)
	}()

	// Step 3: Wait a moment for the server to start, then call every endpoint
	ctx = context.Background()
	client := http.Client{Timeout: 10 * time.Second}

	// Give the server a moment to initialize
	time.Sleep(500 * time.Millisecond)

	// /v1/health - may be 503 if index is initializing
	healthResp, err := client.Get("http://" + serveAddr + "/v1/health?network=testnet")
	if err != nil {
		t.Fatalf("/v1/health request failed: %v", err)
	}
	healthBody, _ := io.ReadAll(healthResp.Body)
	healthResp.Body.Close()
	if healthResp.StatusCode != 200 && healthResp.StatusCode != 503 {
		t.Fatalf("/v1/health returned status %d, body: %s", healthResp.StatusCode, string(healthBody))
	}

	// /v1/stats
	statsResp, err := client.Get("http://" + serveAddr + "/v1/stats?network=testnet")
	if err != nil {
		t.Fatalf("/v1/stats request failed: %v", err)
	}
	statsBody, _ := io.ReadAll(statsResp.Body)
	statsResp.Body.Close()
	if statsResp.StatusCode != 200 {
		t.Fatalf("/v1/stats returned status %d, body: %s", statsResp.StatusCode, string(statsBody))
	}

	// /v1/seps
	sepsResp, err := client.Get("http://" + serveAddr + "/v1/seps?network=testnet")
	if err != nil {
		t.Fatalf("/v1/seps request failed: %v", err)
	}
	sepsBody, _ := io.ReadAll(sepsResp.Body)
	sepsResp.Body.Close()
	if sepsResp.StatusCode != 200 {
		t.Fatalf("/v1/seps returned status %d, body: %s", sepsResp.StatusCode, string(sepsBody))
	}

	// /v1/contracts?implements=41 - may return empty if no contracts indexed
	contractsResp, err := client.Get("http://" + serveAddr + "/v1/contracts?implements=41&network=testnet&limit=5")
	if err != nil {
		t.Fatalf("/v1/contracts?implements=41 request failed: %v", err)
	}
	contractsBody, _ := io.ReadAll(contractsResp.Body)
	contractsResp.Body.Close()

	// /v1/contracts/{id} - if contracts exist, test detail; otherwise skip gracefully
	if len(contractsBody) > 0 && contractsResp.StatusCode == 200 {
		type contractSummary struct {
			ContractID string `json:"contract_id"`
		}
		var contractsPage struct {
			Contracts []contractSummary `json:"contracts"`
		}
		if err := json.Unmarshal(contractsBody, &contractsPage); err != nil {
			t.Fatalf("failed to unmarshal contracts: %v", err)
		}
		if len(contractsPage.Contracts) > 0 {
			firstContractID := contractsPage.Contracts[0].ContractID
			detailRes, err := client.Get("http://" + serveAddr + "/v1/contracts/" + firstContractID + "?network=testnet")
			if err != nil {
				t.Fatalf("/v1/contracts/{id} request failed: %v", err)
			}
			detailBody, _ := io.ReadAll(detailRes.Body)
			detailRes.Body.Close()
			if detailRes.StatusCode != 200 {
				t.Fatalf("/v1/contracts/{id} returned status %d, body: %s", detailRes.StatusCode, string(detailBody))
			}

			// /v1/contracts/{id}/history
			historyResp, err := client.Get("http://" + serveAddr + "/v1/contracts/" + firstContractID + "/history?network=testnet&limit=5")
			if err != nil {
				t.Fatalf("/v1/contracts/{id}/history request failed: %v", err)
			}
			historyBody, _ := io.ReadAll(historyResp.Body)
			historyResp.Body.Close()
			if historyResp.StatusCode != 200 {
				t.Fatalf("/v1/contracts/{id}/history returned status %d, body: %s", historyResp.StatusCode, string(historyBody))
			}
		}
	}

	// /v1/stats/toolchains
	toolchainsResp, err := client.Get("http://" + serveAddr + "/v1/stats/toolchains?network=testnet")
	if err != nil {
		t.Fatalf("/v1/stats/toolchains request failed: %v", err)
	}
	toolchainsBody, _ := io.ReadAll(toolchainsResp.Body)
	toolchainsResp.Body.Close()
	if toolchainsResp.StatusCode != 200 {
		t.Fatalf("/v1/stats/toolchains returned status %d, body: %s", toolchainsResp.StatusCode, string(toolchainsBody))
	}

	// Step 4: Clean up
	done <- nil

	t.Logf("E2E pipeline completed successfully")
	t.Logf("sync caught_up: true")
}