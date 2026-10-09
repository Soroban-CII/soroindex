package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
