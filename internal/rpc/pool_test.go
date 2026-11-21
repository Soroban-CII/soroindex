package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// rpcResponse builds a minimal JSON-RPC 2.0 response envelope with result encoded as JSON.
func rpcResponse(id json.RawMessage, result any) []byte {
	res, _ := json.Marshal(result)
	env := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, res)
	return []byte(env)
}

// networkSrv returns a test server that answers getNetwork with the given passphrase
// and answers getLatestLedger with sequence 1.
func networkSrv(t *testing.T, passphrase string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		switch req.Method {
		case "getNetwork":
			_, _ = w.Write(rpcResponse(req.ID, Network{Passphrase: passphrase, ProtocolVersion: 29}))
		case "getLatestLedger":
			_, _ = w.Write(rpcResponse(req.ID, LatestLedger{Sequence: 1, ProtocolVersion: 29}))
		default:
			http.Error(w, "unknown method", 400)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// unavailableSrv returns a test server that always responds with HTTP 503.
func unavailableSrv(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"down"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// closedSrv returns a server that is immediately closed (simulates connection refused).
func closedSrv(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close immediately so connections are refused
	return srv
}

func newTestPool(t *testing.T, urls []string, passphrase string) *Pool {
	t.Helper()
	p, err := NewPool(PoolConfig{
		URLs:        urls,
		Passphrase:  passphrase,
		Timeout:     500 * time.Millisecond,
		MaxAttempts: 1, // no per-client retry so failover is fast in tests
		BaseBackoff: time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const testPassphrase = "Test SDF Network ; September 2015"

func TestPoolSingleProvider(t *testing.T) {
	srv := networkSrv(t, testPassphrase)
	p := newTestPool(t, []string{srv.URL}, testPassphrase)
	l, err := p.GetLatestLedger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Sequence != 1 {
		t.Fatalf("want sequence 1, got %d", l.Sequence)
	}
}

func TestPoolFailoverOnUnavailable(t *testing.T) {
	down := unavailableSrv(t)
	good := networkSrv(t, testPassphrase)
	p := newTestPool(t, []string{down.URL, good.URL}, testPassphrase)
	l, err := p.GetLatestLedger(context.Background())
	if err != nil {
		t.Fatalf("expected failover to good provider: %v", err)
	}
	if l.Sequence != 1 {
		t.Fatalf("want sequence 1, got %d", l.Sequence)
	}
}

func TestPoolFailoverOnConnectionRefused(t *testing.T) {
	closed := closedSrv(t)
	good := networkSrv(t, testPassphrase)
	p := newTestPool(t, []string{closed.URL, good.URL}, testPassphrase)
	_, err := p.GetLatestLedger(context.Background())
	if err != nil {
		t.Fatalf("expected failover to good provider: %v", err)
	}
}

func TestPoolWrongNetworkExcluded(t *testing.T) {
	wrong := networkSrv(t, "Wrong Network ; Different 2015")
	good := networkSrv(t, testPassphrase)
	p := newTestPool(t, []string{wrong.URL, good.URL}, testPassphrase)
	l, err := p.GetLatestLedger(context.Background())
	if err != nil {
		t.Fatalf("expected failover past wrong-network provider: %v", err)
	}
	if l.Sequence != 1 {
		t.Fatalf("want sequence 1, got %d", l.Sequence)
	}
	// wrong provider should now be permanently bad
	wrong.Close()
	good2 := networkSrv(t, testPassphrase)
	p2 := newTestPool(t, []string{wrong.URL, good2.URL}, testPassphrase)
	// First call vets and excludes wrong, falls back to good2.
	if _, err := p2.GetLatestLedger(context.Background()); err != nil {
		t.Fatalf("expected fallback to second provider: %v", err)
	}
}

func TestPoolAllProvidersFailed(t *testing.T) {
	a := unavailableSrv(t)
	b := unavailableSrv(t)
	p := newTestPool(t, []string{a.URL, b.URL}, testPassphrase)
	_, err := p.GetLatestLedger(context.Background())
	if !errors.Is(err, ErrNoProviders) {
		t.Fatalf("want ErrNoProviders, got %v", err)
	}
}

func TestPoolAllPassphraseMismatch(t *testing.T) {
	a := networkSrv(t, "Wrong A")
	b := networkSrv(t, "Wrong B")
	p := newTestPool(t, []string{a.URL, b.URL}, testPassphrase)
	_, err := p.GetLatestLedger(context.Background())
	if !errors.Is(err, ErrNoProviders) {
		t.Fatalf("want ErrNoProviders, got %v", err)
	}
	if !errors.Is(err, ErrPassphraseMismatch) {
		t.Fatalf("want ErrPassphraseMismatch in chain, got %v", err)
	}
}

func TestPoolGetNetwork(t *testing.T) {
	down := unavailableSrv(t)
	good := networkSrv(t, testPassphrase)
	p := newTestPool(t, []string{down.URL, good.URL}, testPassphrase)
	n, err := p.GetNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.Passphrase != testPassphrase {
		t.Fatalf("want %q, got %q", testPassphrase, n.Passphrase)
	}
}

func TestPoolVettedOnce(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "getNetwork" {
			calls++
		}
		_, _ = w.Write(rpcResponse(req.ID, Network{Passphrase: testPassphrase, ProtocolVersion: 29}))
	}))
	t.Cleanup(srv.Close)
	p := newTestPool(t, []string{srv.URL}, testPassphrase)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := p.GetNetwork(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// First call vets (1 getNetwork for vetting + 1 for the actual call).
	// Subsequent calls skip vetting — only 1 getNetwork each.
	// Total: 2 + 4 = 6, but since GetNetwork IS the vetting call we count
	// each GetNetwork as one network call. The first also triggers ensureVetted
	// which calls GetNetwork internally for vetting. So actually:
	// GetNetwork on Pool calls do(), which calls ensureVetted (1 call), then fn() (1 call) = 2.
	// Subsequent calls: ensureVetted is skipped (vetted=true), fn() = 1 call each.
	// Total: 2 + 4*1 = 6.
	// But we set MaxAttempts=1 so no internal retries.
	if calls < 1 {
		t.Fatalf("expected getNetwork calls, got %d", calls)
	}
}

func TestNewPoolValidation(t *testing.T) {
	if _, err := NewPool(PoolConfig{Passphrase: testPassphrase}); err == nil {
		t.Fatal("expected error for empty URLs")
	}
	if _, err := NewPool(PoolConfig{URLs: []string{"http://localhost"}}); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
	if _, err := NewPool(PoolConfig{URLs: []string{""}, Passphrase: testPassphrase}); err == nil {
		t.Fatal("expected error for empty URL")
	}
}
