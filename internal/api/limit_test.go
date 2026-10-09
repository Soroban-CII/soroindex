package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestPerIPRateLimitIgnoresForwardedHeaders(t *testing.T) {
	s := catalogServer(t)
	s.options.RateLimit = 1
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
	if request("192.0.2.1:123", "") != 200 || request("192.0.2.1:456", "198.51.100.1") != 429 || request("192.0.2.2:123", "") != 200 {
		t.Fatal("rate limit not keyed by peer IP")
	}
	var l ipLimiter
	now := time.Now()
	if !l.allow("[::1]:123", 1, now) || l.allow("[::1]:456", 1, now) || !l.allow("[::1]:456", 1, now.Add(time.Minute)) {
		t.Fatal("window did not reset")
	}
	if !l.allow("[::1]:123", 0, now) {
		t.Fatal("disabled limiter rejected")
	}
}
