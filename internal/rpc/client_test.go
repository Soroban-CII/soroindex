package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// recorded returns a recorded response body, with its "id" rewritten to id
// so it answers the request being served.
func recorded(t *testing.T, name string, id json.RawMessage) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- fixed test file names
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["id"] = id
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type rpcReq struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func readReq(t *testing.T, r *http.Request) rpcReq {
	t.Helper()
	var q rpcReq
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return q
}

// newTestClient returns a client for srv with instant backoff.
func newTestClient(t *testing.T, srv *httptest.Server, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{URL: srv.URL, Timeout: 2 * time.Second, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func serveRecorded(t *testing.T, file string, check func(rpcReq)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		if check != nil {
			check(q)
		}
		_, _ = w.Write(recorded(t, file, q.ID))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetNetworkRecorded(t *testing.T) {
	srv := serveRecorded(t, "getNetwork.json", func(q rpcReq) {
		if q.Method != "getNetwork" {
			t.Errorf("method = %q", q.Method)
		}
	})
	n, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.Passphrase != "Test SDF Network ; September 2015" || n.ProtocolVersion != 29 {
		t.Fatalf("got %+v", n)
	}
}

func TestGetLatestLedgerRecorded(t *testing.T) {
	srv := serveRecorded(t, "getLatestLedger.json", nil)
	l, err := newTestClient(t, srv, nil).GetLatestLedger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Sequence != 5062912 || l.ProtocolVersion != 29 || l.CloseTime != "1791338147" {
		t.Fatalf("got %+v", l)
	}
}

func TestGetLedgersRecorded(t *testing.T) {
	srv := serveRecorded(t, "getLedgers.json", func(q rpcReq) {
		var p map[string]any
		_ = json.Unmarshal(q.Params, &p)
		pg, _ := p["pagination"].(map[string]any)
		if p["startLedger"] != float64(5062907) || pg["limit"] != float64(2) {
			t.Errorf("params = %s", q.Params)
		}
	})
	page, err := newTestClient(t, srv, nil).GetLedgers(context.Background(), LedgersRequest{StartLedger: 5062907, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Ledgers) != 2 || page.Ledgers[0].Sequence != 5062907 || page.Ledgers[1].Sequence != 5062908 {
		t.Fatalf("ledgers = %d", len(page.Ledgers))
	}
	if page.OldestLedger != 4941953 || page.LatestLedger != 5062912 || page.Cursor != "5062908" {
		t.Fatalf("window/cursor = %d..%d %q", page.OldestLedger, page.LatestLedger, page.Cursor)
	}
	// The recorded metadata is real LedgerCloseMeta for the stated ledger.
	var meta xdr.LedgerCloseMeta
	if err := xdr.SafeUnmarshalBase64(page.Ledgers[0].MetadataXDR, &meta); err != nil {
		t.Fatalf("decode metadataXdr: %v", err)
	}
	if got := meta.LedgerSequence(); got != 5062907 {
		t.Fatalf("meta ledger = %d", got)
	}
}

func TestGetLedgerEntriesRequestShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		if q.Method != "getLedgerEntries" || string(q.Params) != `{"keys":["AAAA"]}` {
			t.Errorf("request = %s %s", q.Method, q.Params)
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(q.ID)+`,"result":{"entries":[{"key":"AAAA","xdr":"BBBB","lastModifiedLedgerSeq":7,"liveUntilLedgerSeq":99}],"latestLedger":100}}`)
	}))
	defer srv.Close()
	got, err := newTestClient(t, srv, nil).GetLedgerEntries(context.Background(), []string{"AAAA"})
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestLedger != 100 || len(got.Entries) != 1 || *got.Entries[0].LiveUntilLedgerSeq != 99 {
		t.Fatalf("got %+v", got)
	}
}

func TestArgumentValidation(t *testing.T) {
	c, err := New(Config{URL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"getLedgers needs start or cursor", func() error { _, e := c.GetLedgers(ctx, LedgersRequest{Limit: 1}); return e }},
		{"getLedgers rejects both start and cursor", func() error {
			_, e := c.GetLedgers(ctx, LedgersRequest{StartLedger: 1, Cursor: "x", Limit: 1})
			return e
		}},
		{"getLedgers rejects limit 0", func() error { _, e := c.GetLedgers(ctx, LedgersRequest{StartLedger: 1}); return e }},
		{"getLedgers rejects limit 201", func() error { _, e := c.GetLedgers(ctx, LedgersRequest{StartLedger: 1, Limit: 201}); return e }},
		{"getLedgerEntries rejects no keys", func() error { _, e := c.GetLedgerEntries(ctx, nil); return e }},
		{"getLedgerEntries rejects 201 keys", func() error { _, e := c.GetLedgerEntries(ctx, make([]string, 201)); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.call() == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("New without URL: want error")
	}
}

// statusServer answers each request with the next status in seq; after the
// sequence ends it answers 200 with a getNetwork result.
func statusServer(t *testing.T, seq []int, hdr map[string]string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		i := int(n.Add(1)) - 1
		if i < len(seq) {
			for k, v := range hdr {
				w.Header().Set(k, v)
			}
			w.WriteHeader(seq[i])
			return
		}
		_, _ = w.Write(recorded(t, "getNetwork.json", q.ID))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRetries(t *testing.T) {
	tests := []struct {
		name      string
		seq       []int
		wantErr   bool
		wantCalls int32
	}{
		{"429 is retried", []int{429}, false, 2},
		{"500 then 503 are retried", []int{500, 503}, false, 3},
		{"5xx gives up after 6 attempts", []int{500, 500, 500, 500, 500, 500, 500}, true, 6},
		{"400 is not retried", []int{400}, true, 1},
		{"404 is not retried", []int{404}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, calls := statusServer(t, tt.seq, nil)
			_, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrHTTP) {
				t.Fatalf("err = %v, want ErrHTTP", err)
			}
			if calls.Load() != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestRetryAfterIsHonoredUpToMaxBackoff(t *testing.T) {
	srv, _ := statusServer(t, []int{429}, map[string]string{"Retry-After": "3600"})
	c := newTestClient(t, srv, nil)
	var waited []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }
	if _, err := c.GetNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(waited) != 1 || waited[0] != c.cfg.MaxBackoff {
		t.Fatalf("waited %v, want one wait of MaxBackoff %v", waited, c.cfg.MaxBackoff)
	}
}

func TestBackoffIsBoundedAndGrows(t *testing.T) {
	c, _ := New(Config{URL: "x", BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second})
	for attempt := 1; attempt <= 40; attempt++ {
		ceil := min(100*time.Millisecond<<min(attempt-1, 30), time.Second)
		for i := 0; i < 50; i++ {
			if d := c.backoff(attempt); d < 0 || d > ceil {
				t.Fatalf("attempt %d: backoff %v outside [0, %v]", attempt, d, ceil)
			}
		}
	}
}

func TestPerAttemptTimeoutIsRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		if n.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond) // first attempt outlives its timeout
		}
		_, _ = w.Write(recorded(t, "getNetwork.json", q.ID))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(cfg *Config) { cfg.Timeout = 100 * time.Millisecond })
	if _, err := c.GetNetwork(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 2 {
		t.Fatalf("calls = %d, want 2", n.Load())
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	srv, calls := statusServer(t, []int{500, 500, 500, 500, 500, 500}, nil)
	c := newTestClient(t, srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	_, err := c.GetNetwork(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    func(id string) string
		wantErr error
		wantRPC int
	}{
		{"JSON-RPC error is returned with its code and not retried", func(id string) string {
			return string(recorded(t, "getLedgers_out_of_range.json", json.RawMessage(id)))
		}, nil, -32600},
		{"mismatched id is a protocol error", func(string) string {
			return `{"jsonrpc":"2.0","id":999999,"result":{}}`
		}, ErrProtocol, 0},
		{"missing result is a protocol error", func(id string) string {
			return `{"jsonrpc":"2.0","id":` + id + `}`
		}, ErrProtocol, 0},
		{"invalid JSON is a protocol error", func(string) string { return `{not json` }, ErrProtocol, 0},
		{"wrong result type is a protocol error", func(id string) string {
			return `{"jsonrpc":"2.0","id":` + id + `,"result":{"passphrase":7}}`
		}, ErrProtocol, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, tt.body(string(readReq(t, r).ID)))
			}))
			defer srv.Close()
			_, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			var re *Error
			if tt.wantRPC != 0 && (!errors.As(err, &re) || re.Code != tt.wantRPC) {
				t.Fatalf("err = %v, want rpc code %d", err, tt.wantRPC)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1 (no retry)", calls.Load())
			}
		})
	}
}

func TestResponseSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(q.ID)+`,"result":{"passphrase":"`+strings.Repeat("x", 4096)+`"}}`)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv, func(c *Config) { c.MaxResponseBytes = 1024 }).GetNetwork(context.Background())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := readReq(t, r)
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = w.Write(recorded(t, "getNetwork.json", q.ID))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *Config) { c.Concurrency = 3 })
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.GetNetwork(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if p := peak.Load(); p > 3 || p < 2 {
		t.Fatalf("peak in-flight = %d, want 2..3 with Concurrency 3", p)
	}
}
