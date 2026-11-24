package rpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hostile serves body for every request and counts requests.
func hostile(t *testing.T, h func(w http.ResponseWriter, id string)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		q := readReq(t, r)
		h(w, string(q.ID))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// A declared Content-Length over the cap fails before the body is read,
// and is not retried, so retries never multiply the memory cost.
func TestHugeDeclaredBodyFailsBeforeRead(t *testing.T) {
	var wrote atomic.Int64
	srv, n := hostile(t, func(w http.ResponseWriter, id string) {
		w.Header().Set("Content-Length", strconv.Itoa(64<<20))
		chunk := []byte(strings.Repeat("x", 32<<10))
		for range 2048 { // up to 64 MiB, stopped when the client hangs up
			k, err := w.Write(chunk)
			wrote.Add(int64(k))
			if err != nil {
				return
			}
		}
	})
	_, err := newTestClient(t, srv, func(c *Config) { c.MaxResponseBytes = 1 << 20 }).GetNetwork(context.Background())
	if !errors.Is(err, ErrResponseTooLarge) || !strings.Contains(err.Error(), "Content-Length") {
		t.Fatalf("err = %v, want ErrResponseTooLarge on Content-Length", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1 (too large is not retried)", got)
	}
}

// A chunked body with no declared length is cut off at the cap.
func TestHugeChunkedBodyIsCapped(t *testing.T) {
	srv, n := hostile(t, func(w http.ResponseWriter, id string) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+id+`,"result":{"passphrase":"`)
		w.(http.Flusher).Flush() // forces chunked encoding: no Content-Length
		chunk := strings.Repeat("x", 32<<10)
		for range 256 {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}}`)
	})
	_, err := newTestClient(t, srv, func(c *Config) { c.MaxResponseBytes = 1 << 20 }).GetNetwork(context.Background())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestDeepNestingRejected(t *testing.T) {
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	srv, n := hostile(t, func(w http.ResponseWriter, id string) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+id+`,"result":{"passphrase":"p","x":`+deep+`}}`)
	})
	_, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
	if !errors.Is(err, ErrProtocol) || !strings.Contains(err.Error(), "nested deeper than 64") {
		t.Fatalf("err = %v, want ErrProtocol for nesting", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

// Brackets inside strings are data, not nesting.
func TestBracketsInStringsAreNotNesting(t *testing.T) {
	s := strings.Repeat(`[{\"`, 1000)
	srv, _ := hostile(t, func(w http.ResponseWriter, id string) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+id+`,"result":{"passphrase":"`+s+`"}}`)
	})
	got, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Passphrase, `[{"`) {
		t.Fatalf("passphrase = %.20q", got.Passphrase)
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	srv, _ := hostile(t, func(w http.ResponseWriter, id string) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + id + `,"result":{"passphrase":"bad ` + "\xff\xfe" + `"}}`))
	})
	_, err := newTestClient(t, srv, nil).GetNetwork(context.Background())
	if !errors.Is(err, ErrProtocol) || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("err = %v, want ErrProtocol for invalid UTF-8", err)
	}
}

// A body that trickles past the per-attempt timeout fails that attempt; the
// caller gets an error, not a hang.
func TestSlowTrickleTimesOut(t *testing.T) {
	srv, n := hostile(t, func(w http.ResponseWriter, id string) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+id+`,"result":{"passphrase":"`)
		w.(http.Flusher).Flush()
		for range 200 {
			if _, err := io.WriteString(w, "x"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})
	start := time.Now()
	_, err := newTestClient(t, srv, func(c *Config) { c.Timeout = 100 * time.Millisecond; c.MaxAttempts = 2 }).GetNetwork(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read body") {
		t.Fatalf("err = %v, want a read-body timeout", err)
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2 (timeouts are retried up to MaxAttempts)", got)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("took %v; per-attempt timeout not applied to the body", d)
	}
}

func TestCheckDepth(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		fail bool
	}{
		{`{}`, 1, false},
		{`{"a":[]}`, 1, true},
		{`{"a":[]}`, 2, false},
		{`"[[[["`, 1, false},
		{`{"a":"\"[["}`, 1, false},
		{`{"a":"\\"}`, 1, false},
	} {
		if err := checkDepth([]byte(tc.in), tc.max); (err != nil) != tc.fail {
			t.Errorf("checkDepth(%s, %d) = %v, want fail=%v", tc.in, tc.max, err, tc.fail)
		}
	}
}
