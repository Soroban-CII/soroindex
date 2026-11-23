package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
)

type tip struct {
	sequence uint32
	err      error
}

func (t tip) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: t.sequence}, t.err
}
func apiStore(t *testing.T, prepare func(*store.Store)) *store.Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "api.db")
	w, err := store.Open(ctx, path, store.Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(w)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := store.Open(ctx, path, store.Options{Passphrase: "test", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}
func serve(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}
func TestHealthReportsFreshTipAndReadOnlyStore(t *testing.T) {
	ro := apiStore(t, func(w *store.Store) {
		if err := w.SetState(context.Background(), store.KeyLastLedger, "100"); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 103}})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, s, "/v1/health")
	var h healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || h.Lag != 3 || h.LastLedger != 100 || h.LatestLedger != 103 || h.Status != "ok" {
		t.Fatalf("%d %+v", w.Code, h)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS")
	}
	if err := ro.SetState(context.Background(), "forbidden", "write"); err == nil {
		t.Fatal("API store can write")
	}
	s.backends["testnet"] = Backend{Store: ro, Network: "testnet", RPC: tip{err: errors.New("sensitive provider detail")}}
	w = serve(t, s, "/v1/health")
	if w.Code != 503 || w.Body.String() != "{\"error\":{\"code\":\"internal\",\"message\":\"latest ledger unavailable\"}}\n" {
		t.Fatalf("unsafe error: %d %s", w.Code, w.Body.String())
	}
}
func TestServerRoutingAndMissingProgress(t *testing.T) {
	s, err := New(Options{Store: apiStore(t, nil), Network: "testnet", RPC: tip{sequence: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(t, s, "/v1/health"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := serve(t, s, "/unknown"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/health", nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("OPTIONS", "/v1/health", nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
}

func TestUpgradesSSEStreaming(t *testing.T) {
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Contract 1: initial version at ledger 10, upgrade at ledger 20
		id1 := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAK"
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id1, Kind: "wasm", CurrentWasmHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UpdatedLedger: 20}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: "0000000000000000000000000000000000000000000000000000000000000001", FromLedger: 10}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FromLedger: 20}); err != nil {
			t.Fatal(err)
		}
		// Contract 2: version at ledger 15
		id2 := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB"
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id2, Kind: "wasm_ref", CurrentWasmHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExecRefOwner: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAC", ExecRefTag: "v1", UpdatedLedger: 15}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id2, WasmHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExecRefOwner: "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAC", ExecRefTag: "v1", FromLedger: 15}); err != nil {
			t.Fatal(err)
		}
		if err := tx.SetLastLedger(ctx, 20); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 25}})
	if err != nil {
		t.Fatal(err)
	}

	// Use a real listener and client to test streaming.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	// Start server in background with fast ticker for testing.
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), TestTickerIntervalKey, 10*time.Millisecond)
		s.ServeHTTP(w, r.WithContext(ctx))
	})
	go func() {
		server := &http.Server{Handler: testHandler, ReadHeaderTimeout: 5 * time.Second}
		_ = server.Serve(listener)
	}()

	// Test basic streaming from beginning.
	client := &http.Client{Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/v1/events/upgrades?since=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("wrong content type: %s", resp.Header.Get("Content-Type"))
	}

	// Read events with a deadline.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "retry: 5000") {
		t.Fatalf("missing retry comment: %s", bodyStr)
	}
	// Should have version change events for ledgers 10, 15, 20.
	events := strings.Count(bodyStr, "data: ")
	if events < 3 {
		t.Fatalf("expected at least 3 events, got %d: %s", events, bodyStr)
	}

	// Test resume via since parameter - only events after ledger 15.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/v1/events/upgrades?since=15", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	bodyStr = string(body)
	events = strings.Count(bodyStr, "data: ")
	if events < 1 { // Should have ledger 20 event
		t.Fatalf("expected at least 1 event after ledger 15, got %d: %s", events, bodyStr)
	}

	// Test resume via Last-Event-ID header.
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/v1/events/upgrades", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "15")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	bodyStr = string(body)
	events = strings.Count(bodyStr, "data: ")
	if events < 1 {
		t.Fatalf("expected at least 1 event after Last-Event-ID 15, got %d: %s", events, bodyStr)
	}

	// Test invalid since parameter.
	resp, err = client.Get("http://" + listener.Addr().String() + "/v1/events/upgrades?since=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid since, got %d", resp.StatusCode)
	}

	// Test non-GET method.
	req, err = http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/events/upgrades", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405 for POST, got %d", resp.StatusCode)
	}
}

// TestUpgradesSSECancellation tests that the stream respects context cancellation.
func TestUpgradesSSECancellation(t *testing.T) {
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id1 := "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAK"
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id1, Kind: "wasm", CurrentWasmHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UpdatedLedger: 10}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: "0000000000000000000000000000000000000000000000000000000000000001", FromLedger: 10}); err != nil {
			t.Fatal(err)
		}
		if err := tx.SetLastLedger(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 20}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/events/upgrades?since=0", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	// Start serving in a goroutine and cancel after a short delay.
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(w, req)
		close(done)
	}()

	// Give it time to send initial response.
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Should complete without hanging.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not exit after context cancellation")
	}
}
