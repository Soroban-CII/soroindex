package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Soroban-CII/soroindex/internal/store"
)

// leaksInternals checks that a response body does not expose file paths,
// raw SQL text, or Go type names (e.g. from an unhandled panic recovery).
var leaksInternals = []string{
	"sql:", "sqlite", ".go:", "runtime/", "goroutine ", "panic:",
	"SELECT ", "INSERT ", "WHERE ", "FROM ",
	"github.com/", "internal/",
}

// containsHTTPSep returns true for any rune that would break httptest.NewRequest
// URL parsing: space, control characters, DEL (RFC 7230 request-target rules).
func containsHTTPSep(s string) bool {
	for _, c := range s {
		if c <= ' ' || c == 0x7F {
			return true
		}
	}
	return false
}

func bodyLeaksInternals(body string) string {
	up := strings.ToUpper(body)
	for _, needle := range leaksInternals {
		if strings.Contains(up, strings.ToUpper(needle)) {
			return needle
		}
	}
	return ""
}

func newFuzzServer(tb testing.TB) *Server {
	tb.Helper()
	ctx := context.Background()
	path := filepath.Join(tb.TempDir(), "fuzz.db")
	w, err := store.Open(ctx, path, store.Options{Passphrase: "test"})
	if err != nil {
		tb.Fatal(err)
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	ro, err := store.Open(ctx, path, store.Options{Passphrase: "test", ReadOnly: true})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = ro.Close() })
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 10}})
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

func fuzzRequest(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// acceptableStatus returns true for any status code this endpoint is
// documented to produce.
func acceptableStatus(code int) bool {
	switch code {
	case 200, 301, 302, 307, 308, 400, 404, 405, 429:
		return true
	}
	return false
}

func checkResponse(t *testing.T, body string, code int) {
	t.Helper()
	if !acceptableStatus(code) {
		t.Errorf("unexpected status %d, body: %s", code, body)
	}
	if code != 200 {
		return
	}
	if !json.Valid([]byte(body)) {
		t.Errorf("non-JSON 200 body: %s", body)
	}
	if needle := bodyLeaksInternals(body); needle != "" {
		t.Errorf("response leaks internal detail %q in: %s", needle, body)
	}
}

// FuzzContractsQueryParams fuzzes every recognised query-parameter value
// on GET /v1/contracts. Each parameter is exercised independently so a
// single bad value cannot hide a second.
func FuzzContractsQueryParams(f *testing.F) {
	s := newFuzzServer(f)

	params := []string{
		"implements=", "implements_any=", "tier=", "kind=",
		"limit=", "cursor=", "expiring_before=",
	}
	// Seed corpus: values that are always valid or always invalid.
	seeds := []string{
		"41", "41,46", "0", "-1", "declared", "inferred", "wasm", "sac",
		"wasm_ref", "1", "500", "501", "abc", "", "%27+OR+1%3D1--",
		strings.Repeat("a", 1000),
	}
	for _, p := range params {
		for _, v := range seeds {
			f.Add(p + v)
		}
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if !utf8.ValidString(raw) || containsHTTPSep(raw) {
			t.Skip()
		}
		w := fuzzRequest(s, "/v1/contracts?"+raw)
		checkResponse(t, w.Body.String(), w.Code)
	})
}

// FuzzContractByID fuzzes the contract ID path segment.
func FuzzContractByID(f *testing.F) {
	s := newFuzzServer(f)

	f.Add("CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU") // valid strkey
	f.Add("not-a-strkey")
	f.Add("")
	f.Add(strings.Repeat("C", 60))
	f.Add("';DROP-TABLE-contracts;--")

	f.Fuzz(func(t *testing.T, id string) {
		if !utf8.ValidString(id) || containsHTTPSep(id) {
			t.Skip()
		}
		w := fuzzRequest(s, "/v1/contracts/"+id)
		checkResponse(t, w.Body.String(), w.Code)
	})
}

// FuzzWasmByHash fuzzes the Wasm hash path segment.
func FuzzWasmByHash(f *testing.F) {
	s := newFuzzServer(f)

	f.Add("1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96") // valid hex
	f.Add("not-hex")
	f.Add("")
	f.Add(strings.Repeat("a", 64))
	f.Add(strings.Repeat("Z", 64))

	f.Fuzz(func(t *testing.T, hash string) {
		if !utf8.ValidString(hash) || containsHTTPSep(hash) {
			t.Skip()
		}
		w := fuzzRequest(s, "/v1/wasm/"+hash)
		checkResponse(t, w.Body.String(), w.Code)
	})
}

// FuzzContractHistoryID fuzzes the contract ID on the history endpoint.
func FuzzContractHistoryID(f *testing.F) {
	s := newFuzzServer(f)

	f.Add("CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU")
	f.Add("bad")
	f.Add("")

	f.Fuzz(func(t *testing.T, id string) {
		if !utf8.ValidString(id) || containsHTTPSep(id) {
			t.Skip()
		}
		w := fuzzRequest(s, "/v1/contracts/"+id+"/history")
		checkResponse(t, w.Body.String(), w.Code)
	})
}
