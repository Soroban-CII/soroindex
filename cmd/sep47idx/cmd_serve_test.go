package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/api"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestServeHTTPReadsIndexAndShutsDown(t *testing.T) {
	db, _, _ := readFixture(t)
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	handler, err := api.New(api.Options{Store: s, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, listener, handler) }()
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var d store.Statistics
	if err = json.NewDecoder(response.Body).Decode(&d); err != nil || response.StatusCode != 200 || d.TotalContracts != 1 {
		t.Fatal(d, err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestServeCLIRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--rate-limit", "-1"},
		{"serve", "extra"},
		{"serve", "--db", "/missing/index.db"},
		{"serve", "--network", "mainnet"},
		{"serve", "--db", "mainnet=/a.db,testnet=/b.db"},
		{"serve", "--db", "mainnet=/a.db,testnet=/b.db", "--rpc-url", "mainnet=http://127.0.0.1:1", "--network", "testnet"},
	} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}

func TestMultiNetworkHandlerServesBothIndexes(t *testing.T) {
	testnetDB := ledgerDB(t, config.Testnet, 10)
	mainnetDB := ledgerDB(t, config.Mainnet, 20)
	testnetRPC := networkRPC(t, config.Testnet.Passphrase, 12)
	mainnetRPC := networkRPC(t, config.Mainnet.Passphrase, 22)
	cfg := config.Common{
		DB:          "testnet=" + testnetDB + ",mainnet=" + mainnetDB,
		RPCURL:      "testnet=" + testnetRPC.URL + ",mainnet=" + mainnetRPC.URL,
		RPCTimeout:  time.Second,
		Concurrency: 1,
	}
	h, cleanup, err := multiNetworkHandler(context.Background(), cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for network, want := range map[string]uint32{"testnet": 10, "mainnet": 20} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health?network="+network, nil))
		var got struct {
			Network    string `json:"network"`
			LastLedger uint32 `json:"last_ledger"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Network != network || got.LastLedger != want {
			t.Fatalf("%s: %d %s (%v)", network, w.Code, w.Body.String(), err)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing network: %d %s", w.Code, w.Body.String())
	}

	cfg.RPCURL = "testnet=" + testnetRPC.URL
	if _, _, err := multiNetworkHandler(context.Background(), cfg, 0); err == nil {
		t.Fatal("mismatched network maps accepted")
	}
	cfg.RPCURL = "mainnet=" + mainnetRPC.URL + ",testnet=" + testnetRPC.URL
	cfg.DB = "mainnet=" + mainnetDB
	if _, _, err := multiNetworkHandler(context.Background(), cfg, 0); err == nil {
		t.Fatal("single network map accepted")
	}
}

func ledgerDB(t *testing.T, network config.Network, ledger uint32) string {
	t.Helper()
	db := t.TempDir() + "/" + network.Name + ".db"
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: network.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(context.Background(), store.KeyLastLedger, strconv.FormatUint(uint64(ledger), 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

func networkRPC(t *testing.T, passphrase string, latest uint32) *httptest.Server {
	t.Helper()
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
			result = map[string]any{"passphrase": passphrase, "protocolVersion": 29}
		case "getLatestLedger":
			result = map[string]any{"sequence": latest}
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
