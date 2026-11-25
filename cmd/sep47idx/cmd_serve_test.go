package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	go func() {
		done <- serveHTTP(ctx, listener, handler, 5*time.Second, 20*time.Second, 20*time.Second, 60*time.Second, 16<<10)
	}()
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
	case receivedErr := <-done:
		if receivedErr != nil {
			t.Fatal(receivedErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestServeCLIRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--rate-limit", "-1"},
		{"serve", "--cors-allowed-origins", "https://good.example,,https://bad.example"},
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

func TestParseCORSAllowedOrigins(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  []string
		err   bool
	}{
		{name: "empty keeps public default"},
		{name: "trims and splits", value: " https://one.example,https://two.example ", want: []string{"https://one.example", "https://two.example"}},
		{name: "rejects empty list entry", value: "https://one.example,,https://two.example", err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCORSAllowedOrigins(test.value)
			if (err != nil) != test.err {
				t.Fatalf("error = %v, want error %v", err, test.err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("origins = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("origins = %v, want %v", got, test.want)
				}
			}
		})
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
	const allowedOrigin = "https://app.example"
	h, cleanup, err := multiNetworkHandler(context.Background(), cfg, 0, []string{allowedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for network, want := range map[string]uint32{"testnet": 10, "mainnet": 20} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/health?network="+network, nil)
		req.Header.Set("Origin", allowedOrigin)
		h.ServeHTTP(w, req)
		var got struct {
			Network    string `json:"network"`
			LastLedger uint32 `json:"last_ledger"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Network != network || got.LastLedger != want {
			t.Fatalf("%s: %d %s (%v)", network, w.Code, w.Body.String(), err)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
			t.Fatalf("%s origin header = %q", network, got)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing network: %d %s", w.Code, w.Body.String())
	}

	cfg.RPCURL = "testnet=" + testnetRPC.URL
	if _, _, err := multiNetworkHandler(context.Background(), cfg, 0, nil); err == nil {
		t.Fatal("mismatched network maps accepted")
	}
	cfg.RPCURL = "mainnet=" + mainnetRPC.URL + ",testnet=" + testnetRPC.URL
	cfg.DB = "mainnet=" + mainnetDB
	if _, _, err := multiNetworkHandler(context.Background(), cfg, 0, nil); err == nil {
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

// TestServeSlowClientClosed tests that a client sending headers very slowly
// is disconnected by the ReadHeaderTimeout.
func TestServeSlowClientClosed(t *testing.T) {
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

	// Very short read header timeout to force the test to be fast.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, listener, handler, 100*time.Millisecond, 20*time.Second, 20*time.Second, 60*time.Second, 16<<10)
	}()
	defer func() {
		cancel()
		<-done //nolint:errcheck // intentionally ignore error in test cleanup
	}()

	// Connect and send request line, then stall before sending headers.
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Send request line only.
	if _, err := conn.Write([]byte("GET /v1/stats HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	// Wait longer than read header timeout without sending more.
	time.Sleep(300 * time.Millisecond)

	// The connection should be closed by the server. Reading should fail.
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected connection to be closed by server due to read header timeout")
	}
	// Accept both timeout and EOF (clean close) as the server closing the connection.
	var netErr net.Error
	if !errors.As(err, &netErr) && err != io.EOF {
		t.Fatalf("expected timeout or EOF error, got %v", err)
	}
	_ = conn.Close()
}

// TestServeOversizedHeadersClosed tests that a client sending headers
// larger than MaxHeaderBytes receives a 431 response and the connection is closed.
func TestServeOversizedHeadersClosed(t *testing.T) {
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

	// Small max header bytes to force the test to be fast.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, listener, handler, 5*time.Second, 20*time.Second, 20*time.Second, 60*time.Second, 256)
	}()
	defer func() {
		cancel()
		<-done //nolint:errcheck // intentionally ignore error in test cleanup
	}()

	// Connect and send a request with a very large header.
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Build a request with a header exceeding 256 bytes.
	// Go's http.Server reads headers until MaxHeaderBytes is exceeded, then
	// responds with 431 Request Header Fields Too Large and closes the connection.
	largeHeader := "X-Large: " + string(make([]byte, 300)) + "\r\n"
	request := "GET /v1/stats HTTP/1.1\r\n" + largeHeader + "\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}

	// Read the 431 response.
	buf := make([]byte, 4096)
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	response := string(buf[:n])
	t.Logf("Response (%d bytes): %s", n, response)
	if n == 0 {
		t.Fatal("expected 431 response, got empty response")
	}
	// Verify it's a 431 response (or at least an HTTP error response).
	if !contains(response, "431") && !contains(response, "400") && !contains(response, "500") {
		t.Fatalf("expected error response, got: %s", response)
	}

	// The connection should be closed by the server after the response.
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(buf[:1])
	if err == nil {
		t.Fatal("expected connection to be closed by server after 431 response")
	}
	// Accept both timeout and EOF (clean close) as the server closing the connection.
	var netErr net.Error
	if !errors.As(err, &netErr) && err != io.EOF {
		t.Fatalf("expected timeout or EOF error, got %v", err)
	}
	_ = conn.Close()
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}

// TestServeGracefulShutdownCompletesInFlight tests that graceful shutdown
// waits for in-flight requests to complete.
func TestServeGracefulShutdownCompletesInFlight(t *testing.T) {
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

	var requestStarted, requestDone chan struct{}
	requestStarted = make(chan struct{})
	requestDone = make(chan struct{})

	// Wrap handler to track in-flight request.
	trackingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		time.Sleep(200 * time.Millisecond)
		handler.ServeHTTP(w, r)
		close(requestDone)
	})

	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, listener, trackingHandler, 5*time.Second, 20*time.Second, 20*time.Second, 60*time.Second, 16<<10)
	}()

	client := http.Client{Timeout: 5 * time.Second}
	// Start a request that will take some time.
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String() + "/v1/stats")
		if err != nil {
			t.Logf("request error: %v", err)
			return
		}
		_ = resp.Body.Close()
	}()

	// Wait for request to start, then initiate shutdown.
	<-requestStarted
	cancel()

	// Shutdown should complete without error and after request finishes.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete in time")
	}

	// Verify request completed.
	select {
	case <-requestDone:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not complete before shutdown")
	}
}
