package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// RequestIDHeader carries the ID the server assigned to a request. Incoming
// values are ignored, so a caller cannot put its own text into the logs.
const RequestIDHeader = "X-Request-ID"

// MetricsPath is where Options.Metrics exposes the Prometheus text format.
const MetricsPath = "/metrics"

// latencyBuckets are the fixed upper bounds, in seconds, of every route's
// latency histogram. They end below the 15 s request deadline.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// unmatchedRoute labels requests that were rejected before routing (bad
// network, rate limit, wrong method). A fixed label keeps the metric
// cardinality bounded whatever paths clients send.
const unmatchedRoute = "unmatched"

type routeStats struct {
	counts []uint64 // per latencyBuckets entry, not cumulative
	over   uint64   // slower than the last bucket
	sum    float64
	total  uint64
	byCode map[int]uint64
}

type metrics struct {
	mu     sync.Mutex
	routes map[string]*routeStats
}

func (m *metrics) observe(route string, status int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routes == nil {
		m.routes = map[string]*routeStats{}
	}
	rs := m.routes[route]
	if rs == nil {
		rs = &routeStats{counts: make([]uint64, len(latencyBuckets)), byCode: map[int]uint64{}}
		m.routes[route] = rs
	}
	secs := d.Seconds()
	placed := false
	for i, le := range latencyBuckets {
		if secs <= le {
			rs.counts[i]++
			placed = true
			break
		}
	}
	if !placed {
		rs.over++
	}
	rs.sum += secs
	rs.total++
	rs.byCode[status]++
}

// write renders the Prometheus text exposition format, routes and codes in
// sorted order so the output is stable.
func (m *metrics) write(w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := make([]string, 0, len(m.routes))
	for r := range m.routes {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	if _, err := fmt.Fprint(w, "# HELP soroindex_http_requests_total HTTP requests by route pattern and status code.\n# TYPE soroindex_http_requests_total counter\n"); err != nil {
		return err
	}
	for _, r := range routes {
		rs := m.routes[r]
		codes := make([]int, 0, len(rs.byCode))
		for c := range rs.byCode {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		for _, c := range codes {
			if _, err := fmt.Fprintf(w, "soroindex_http_requests_total{route=%q,code=\"%d\"} %d\n", r, c, rs.byCode[c]); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprint(w, "# HELP soroindex_http_request_duration_seconds HTTP request latency by route pattern.\n# TYPE soroindex_http_request_duration_seconds histogram\n"); err != nil {
		return err
	}
	for _, r := range routes {
		rs := m.routes[r]
		var cum uint64
		for i, le := range latencyBuckets {
			cum += rs.counts[i]
			if _, err := fmt.Fprintf(w, "soroindex_http_request_duration_seconds_bucket{route=%q,le=\"%s\"} %d\n", r, strconv.FormatFloat(le, 'g', -1, 64), cum); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "soroindex_http_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\nsoroindex_http_request_duration_seconds_sum{route=%q} %s\nsoroindex_http_request_duration_seconds_count{route=%q} %d\n",
			r, rs.total, r, strconv.FormatFloat(rs.sum, 'g', -1, 64), r, rs.total); err != nil {
			return err
		}
	}
	return nil
}

// statusWriter records the status for the access log and metrics. It keeps
// http.Flusher so the upgrade stream still flushes.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(b[:])
}

// ServeHTTP assigns a request ID, serves the request, then writes one access
// log line and records the route's metrics. The line has the method, route
// pattern, status, duration and ID only: never the query string (it can hold
// cursors) and never a request body.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := newRequestID()
	w.Header().Set(RequestIDHeader, id)
	if s.options.Metrics && r.Method == http.MethodGet && r.URL.Path == MetricsPath {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = s.metrics.write(w) // connection errors cannot be reported after headers
		return
	}
	sw := &statusWriter{ResponseWriter: w}
	route := s.dispatch(sw, r)
	status := sw.status
	if status == 0 {
		status = http.StatusOK
	}
	if route == "" {
		route = unmatchedRoute
	}
	d := time.Since(start)
	if s.options.Metrics {
		s.metrics.observe(route, status, d)
	}
	logger := s.options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
		slog.String("request_id", id), slog.String("method", r.Method), slog.String("route", route),
		slog.Int("status", status), slog.Float64("duration_ms", float64(d.Microseconds())/1000))
}
