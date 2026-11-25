package api

import (
	"net/http/httptest"
	"testing"
)

func TestPerIPRateLimitIgnoresForwardedHeaders(t *testing.T) {
	s, err := New(Options{Store: apiStore(t, nil), Network: "testnet", RateLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote, forwarded string) int {
		r := httptest.NewRequest("GET", "/v1/stats", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code == 429 && w.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry time")
		}
		return w.Code
	}
	// First request from 192.0.2.1:123 should succeed (1/1 allowance).
	if request("192.0.2.1:123", "") != 200 {
		t.Fatal("first request from IP should succeed")
	}
	// Second request from same IP should be rate limited (1 request per minute).
	if request("192.0.2.1:456", "198.51.100.1") != 429 {
		t.Fatal("second request from same IP should be rate limited")
	}
	// Third request from a different IP should succeed.
	if request("192.0.2.2:123", "") != 200 {
		t.Fatal("request from different IP should succeed")
	}
}
