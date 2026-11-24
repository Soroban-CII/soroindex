package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func observedServer(t *testing.T, metrics bool) (*Server, *bytes.Buffer) {
	t.Helper()
	s := catalogServer(t)
	var logs bytes.Buffer
	s.options.Metrics = metrics
	s.options.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	return s, &logs
}

func TestRequestIDIsAssignedAndNotTakenFromTheClient(t *testing.T) {
	s, _ := observedServer(t, false)
	r := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	r.Header.Set(RequestIDHeader, "client-chosen\nforged")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	id := w.Header().Get(RequestIDHeader)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(id) {
		t.Fatalf("request id %q", id)
	}
	if other := serve(t, s, "/v1/stats").Header().Get(RequestIDHeader); other == id {
		t.Fatal("request IDs repeat")
	}
	if e := serve(t, s, "/v1/stats?limit=1"); e.Code != 400 || e.Header().Get(RequestIDHeader) == "" {
		t.Fatal("error responses must carry a request id")
	}
}

func TestAccessLogHasRouteStatusDurationAndNoQueryOrBody(t *testing.T) {
	s, logs := observedServer(t, false)
	id := catalogID(t, 1)
	cursor := "SECRETCURSOR"
	w := serve(t, s, "/v1/contracts/"+id+"?cursor="+cursor+"&limit=1")
	serve(t, s, "/v1/stats")
	serve(t, s, "/v1/stats?network=nope")
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want one line per request, got %d:\n%s", len(lines), logs.String())
	}
	if strings.Contains(logs.String(), cursor) || strings.Contains(logs.String(), "limit=") || strings.Contains(logs.String(), id) {
		t.Fatalf("access log leaks the query or path: %s", logs.String())
	}
	want := []struct {
		route  string
		status float64
	}{{"GET /v1/contracts/{id}", float64(w.Code)}, {"GET /v1/stats", 200}, {unmatchedRoute, 400}}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["route"] != want[i].route || m["status"] != want[i].status || m["method"] != "GET" || m["request_id"] == "" {
			t.Fatalf("line %d: %v", i, m)
		}
		if _, ok := m["duration_ms"].(float64); !ok {
			t.Fatalf("line %d has no duration: %v", i, m)
		}
	}
}

func TestMetricsIsOffByDefault(t *testing.T) {
	s, _ := observedServer(t, false)
	if w := serve(t, s, MetricsPath); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

// sample is one parsed exposition line.
type sample struct {
	name   string
	labels map[string]string
	value  float64
}

var (
	sampleLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(.*)\})? (\S+)$`)
	labelPair  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)
)

// parseExposition checks the Prometheus text format: comment lines are HELP or
// TYPE, every sample line has a valid name, quoted labels and a float value,
// and every sample's family was declared by a TYPE line first.
func parseExposition(t *testing.T, text string) ([]sample, map[string]string) {
	t.Helper()
	var out []sample
	types := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# HELP ") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# TYPE "); ok {
			f := strings.Fields(rest)
			if len(f) != 2 || (f[1] != "counter" && f[1] != "histogram") {
				t.Fatalf("bad TYPE line %q", line)
			}
			types[f[0]] = f[1]
			continue
		}
		m := sampleLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("not a valid sample line: %q", line)
		}
		v, err := strconv.ParseFloat(m[4], 64)
		if err != nil {
			t.Fatalf("bad value in %q", line)
		}
		labels := map[string]string{}
		consumed := 0
		for _, p := range labelPair.FindAllStringSubmatch(m[3], -1) {
			labels[p[1]] = p[2]
			consumed += len(p[0])
		}
		if n := strings.Count(m[3], ",") + 1; m[3] != "" && (len(labels) != n || consumed+n-1 != len(m[3])) {
			t.Fatalf("bad labels in %q", line)
		}
		family := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(m[1], "_bucket"), "_sum"), "_count")
		if types[m[1]] == "" && types[family] == "" {
			t.Fatalf("sample %q has no TYPE line", line)
		}
		out = append(out, sample{m[1], labels, v})
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("exposition must end with a newline")
	}
	return out, types
}

func TestMetricsExposeCountsAndHistogramsInPrometheusFormat(t *testing.T) {
	s, _ := observedServer(t, true)
	for i := 0; i < 3; i++ {
		serve(t, s, "/v1/stats")
	}
	serve(t, s, "/v1/stats?limit=1")
	serve(t, s, "/v1/seps")
	w := serve(t, s, MetricsPath)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
	samples, types := parseExposition(t, w.Body.String())
	if types["soroindex_http_requests_total"] != "counter" || types["soroindex_http_request_duration_seconds"] != "histogram" {
		t.Fatalf("%v", types)
	}
	counts := map[string]float64{}
	var buckets []float64
	var inf, count float64
	for _, sm := range samples {
		switch {
		case sm.name == "soroindex_http_requests_total":
			counts[sm.labels["route"]+" "+sm.labels["code"]] = sm.value
		case sm.name == "soroindex_http_request_duration_seconds_bucket" && sm.labels["route"] == "GET /v1/stats":
			if sm.labels["le"] == "+Inf" {
				inf = sm.value
			} else {
				buckets = append(buckets, sm.value)
			}
		case sm.name == "soroindex_http_request_duration_seconds_count" && sm.labels["route"] == "GET /v1/stats":
			count = sm.value
		}
	}
	if counts["GET /v1/stats 200"] != 3 || counts["GET /v1/stats 400"] != 1 || counts["GET /v1/seps 200"] != 1 {
		t.Fatalf("%v", counts)
	}
	if len(buckets) != len(latencyBuckets) || inf != 4 || count != 4 {
		t.Fatalf("buckets=%v inf=%v count=%v", buckets, inf, count)
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i] < buckets[i-1] {
			t.Fatalf("buckets are not cumulative: %v", buckets)
		}
	}
	if buckets[len(buckets)-1] > inf {
		t.Fatal("+Inf bucket below the last bucket")
	}
}

func TestMetricsRouteLabelsStayBounded(t *testing.T) {
	s, _ := observedServer(t, true)
	for i := 0; i < 20; i++ {
		serve(t, s, "/v1/contracts/"+catalogID(t, byte(i+1)))
	}
	serve(t, s, "/v1/no/such/"+strconv.Itoa(1))
	body := serve(t, s, MetricsPath).Body.String()
	if strings.Contains(body, "/no/such") || strings.Count(body, `soroindex_http_requests_total{route="GET /v1/contracts/{id}"`) == 0 {
		t.Fatalf("labels must be route patterns:\n%s", body)
	}
}

func TestHistogramPlacesObservationsInFixedBuckets(t *testing.T) {
	var m metrics
	m.observe("r", 200, 3*time.Millisecond)
	m.observe("r", 200, 40*time.Millisecond)
	m.observe("r", 200, 20*time.Second)
	var b bytes.Buffer
	if err := m.write(&b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`le="0.005"} 1`, `le="0.05"} 2`, `le="10"} 2`, `le="+Inf"} 3`, `_count{route="r"} 3`,
	} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %s in\n%s", want, b.String())
		}
	}
}
