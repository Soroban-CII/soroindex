// Package rpc is a JSON-RPC 2.0 client for the four Stellar RPC methods the
// indexer needs: getNetwork, getLatestLedger, getLedgers and
// getLedgerEntries. Field names match developers.stellar.org as of
// 2026-10-07.
//
// Responses are untrusted: bodies are size-capped before decoding, and XDR
// fields are returned as base64 strings for the caller to decode with limits.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Sentinel errors.
var (
	// ErrHTTP wraps a non-2xx HTTP status that was not retried, or was
	// retried until attempts ran out. The error is a *HTTPError.
	ErrHTTP = errors.New("rpc http error")
	// ErrResponseTooLarge means a response body exceeded MaxResponseBytes.
	ErrResponseTooLarge = errors.New("rpc response too large")
	// ErrProtocol means the response was not a valid JSON-RPC reply to the
	// request: bad JSON, a mismatched id, or neither result nor error.
	ErrProtocol = errors.New("rpc protocol error")
)

// Error is a JSON-RPC error object returned by the server. Its Message is
// free text and must not be parsed; use Code.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// HTTPError is a non-2xx HTTP response. It matches ErrHTTP under errors.Is.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("%s: status %d", ErrHTTP, e.Status) }

// Is makes errors.Is(err, ErrHTTP) true for any *HTTPError.
func (e *HTTPError) Is(t error) bool { return t == ErrHTTP }

// Config holds client settings. Zero fields take the defaults shown.
type Config struct {
	URL              string
	Timeout          time.Duration // per attempt; default 30s
	Concurrency      int           // max requests in flight; default 4
	MaxAttempts      int           // including the first; default 6
	BaseBackoff      time.Duration // default 250ms
	MaxBackoff       time.Duration // default 10s
	MaxResponseBytes int64         // default 256 MiB
	HTTPClient       *http.Client  // default: a client with no overall timeout (Timeout applies per attempt)
	Logger           *slog.Logger  // default: slog.Default()
}

// Client is safe for concurrent use.
type Client struct {
	cfg    Config
	sem    chan struct{}
	nextID atomic.Uint64
	// sleep waits for d or until ctx ends; replaced in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

// New returns a Client. It does no I/O.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("rpc: URL is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 6
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 250 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 256 << 20
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Client{cfg: cfg, sem: make(chan struct{}, cfg.Concurrency), sleep: sleepCtx}, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *Error          `json:"error"`
}

// call performs one JSON-RPC call with retries and decodes the result into
// out. It retries on HTTP 429 and 5xx, on transport errors and on per-attempt
// timeouts, with exponential backoff and full jitter. It never retries other
// 4xx statuses or JSON-RPC errors, which would fail the same way again.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()

	id := c.nextID.Add(1)
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%s: encode request: %w", method, err)
	}

	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		var retryAfter time.Duration
		var retry bool
		retry, retryAfter, lastErr = c.attempt(ctx, method, id, body, out)
		if lastErr == nil {
			return nil
		}
		if !retry || ctx.Err() != nil || attempt == c.cfg.MaxAttempts {
			break
		}
		wait := c.backoff(attempt)
		if retryAfter > wait {
			wait = min(retryAfter, c.cfg.MaxBackoff)
		}
		c.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "rpc retry",
			slog.String("method", method), slog.Int("attempt", attempt),
			slog.Duration("wait", wait), slog.String("err", lastErr.Error()))
		if err := c.sleep(ctx, wait); err != nil {
			return fmt.Errorf("%s: %w (last error: %w)", method, err, lastErr)
		}
	}
	return fmt.Errorf("%s: %w", method, lastErr)
}

// backoff returns a full-jitter delay for the given attempt (1-based):
// uniform in [0, min(MaxBackoff, BaseBackoff*2^(attempt-1))].
func (c *Client) backoff(attempt int) time.Duration {
	ceil := c.cfg.BaseBackoff << min(attempt-1, 30)
	if ceil <= 0 || ceil > c.cfg.MaxBackoff {
		ceil = c.cfg.MaxBackoff
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1)) // #nosec G404 -- jitter, not security
}

// attempt performs a single HTTP exchange. It reports whether a failure is
// worth retrying and any Retry-After the server asked for.
func (c *Client) attempt(ctx context.Context, method string, id uint64, body []byte, out any) (retry bool, retryAfter time.Duration, err error) {
	actx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		// Transport errors and per-attempt timeouts are retried; the caller's
		// own cancellation is not (checked by call via ctx.Err()).
		return true, 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // drain a little so the connection can be reused
		_ = resp.Body.Close()                                        // close error carries nothing actionable
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		he := &HTTPError{Status: resp.StatusCode}
		if resp.StatusCode == http.StatusTooManyRequests {
			return true, parseRetryAfter(resp.Header.Get("Retry-After")), he
		}
		return resp.StatusCode >= 500, 0, he
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
	if err != nil {
		return true, 0, fmt.Errorf("read body: %w", err)
	}
	if int64(len(raw)) > c.cfg.MaxResponseBytes {
		return false, 0, fmt.Errorf("%w: over %d bytes", ErrResponseTooLarge, c.cfg.MaxResponseBytes)
	}

	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return false, 0, fmt.Errorf("%w: decode envelope: %w", ErrProtocol, err)
	}
	if string(r.ID) != strconv.FormatUint(id, 10) {
		return false, 0, fmt.Errorf("%w: response id %s, want %d", ErrProtocol, r.ID, id)
	}
	if r.Error != nil {
		return false, 0, r.Error
	}
	if len(r.Result) == 0 || string(r.Result) == "null" {
		return false, 0, fmt.Errorf("%w: no result", ErrProtocol)
	}
	if err := json.Unmarshal(r.Result, out); err != nil {
		return false, 0, fmt.Errorf("%w: decode %s result: %w", ErrProtocol, method, err)
	}
	return false, 0, nil
}

// parseRetryAfter reads a Retry-After header given in seconds. HTTP-date
// values are ignored and the normal backoff applies.
func parseRetryAfter(v string) time.Duration {
	s, err := strconv.Atoi(v)
	if err != nil || s < 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}
