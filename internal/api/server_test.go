package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"

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
	s.options.RPC = tip{err: errors.New("sensitive provider detail")}
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

// TestConcurrentWALReadersDuringSync verifies that multiple readers can query
// the API without errors while a writer is concurrently updating sync_state,
// which is the pattern that occurs during a live sync. SQLite WAL mode must
// allow this without returning "database is locked" errors to any reader.
//
// Run with -race to confirm there are no data races in the store layer.
func TestConcurrentWALReadersDuringSync(t *testing.T) {
	const numReaders = 20
	const numWrites = 50

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wal_concurrent.db")

	// Open a writable store (simulating the sync loop).
	w, err := store.Open(ctx, path, store.Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Seed the initial last_ledger.
	if err := w.SetState(ctx, store.KeyLastLedger, "1"); err != nil {
		t.Fatal(err)
	}

	// Open a read-only store pointing to the same file (simulating the API process).
	ro, err := store.Open(ctx, path, store.Options{Passphrase: "test", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	srv, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 100}})
	if err != nil {
		t.Fatal(err)
	}

	// Writer goroutine: updates last_ledger from 2..numWrites, simulating sync.
	writeErrs := make(chan error, 1)
	go func() {
		for i := 2; i <= numWrites; i++ {
			if err := w.SetState(ctx, store.KeyLastLedger, fmt.Sprint(i)); err != nil {
				writeErrs <- err
				return
			}
		}
		close(writeErrs)
	}()

	// Reader goroutines: each issues a /v1/health request and records the
	// last_ledger it observed.
	type result struct {
		code   int
		ledger uint32
	}
	results := make([]result, numReaders)
	var wg sync.WaitGroup
	for i := 0; i < numReaders; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
			results[i].code = rec.Code
			if rec.Code == 200 {
				var h healthResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &h); err == nil {
					results[i].ledger = h.LastLedger
				}
			}
		}()
	}
	wg.Wait()

	// Writer must have completed without error.
	if err := <-writeErrs; err != nil {
		t.Fatalf("writer error: %v", err)
	}

	// Every reader must have received a 200 (no "database is locked" errors).
	var ledgers []uint32
	for _, r := range results {
		if r.code != 200 {
			t.Errorf("reader got status %d", r.code)
			continue
		}
		ledgers = append(ledgers, r.ledger)
	}

	// Every observed ledger must be a valid snapshot (between 1 and numWrites).
	for _, l := range ledgers {
		if l < 1 || l > numWrites {
			t.Errorf("reader saw ledger %d outside expected range [1,%d]", l, numWrites)
		}
	}

	// p95 of observed ledgers: a crude measure that readers saw recent data.
	if len(ledgers) > 0 {
		sort.Slice(ledgers, func(i, j int) bool { return ledgers[i] < ledgers[j] })
		p95 := ledgers[int(float64(len(ledgers))*0.95)]
		t.Logf("WAL concurrent read p95 last_ledger=%d (max=%d)", p95, numWrites)
	}
}
