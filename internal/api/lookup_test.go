package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestBulkLookupReportsUnknownAndKeepsOrder(t *testing.T) {
	s := catalogServer(t)
	known1, known4 := catalogID(t, 1), catalogID(t, 4)
	unknown := catalogID(t, 99)
	w := serve(t, s, "/v1/contracts?ids="+strings.Join([]string{known4, unknown, known1, known4}, ","))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got store.Lookup
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Contracts) != 2 || got.Contracts[0].ID != known4 || got.Contracts[1].ID != known1 {
		t.Fatalf("found contracts lost request order or duplicates were kept: %s", w.Body.String())
	}
	if got.Contracts[0].Kind != "sac" || got.Contracts[0].WasmHash != nil || got.Contracts[1].Kind != "wasm" {
		t.Fatalf("summaries changed: %s", w.Body.String())
	}
	if len(got.NotFound) != 1 || got.NotFound[0] != unknown {
		t.Fatalf("unknown ID not reported: %s", w.Body.String())
	}
	if got.AsOfLedger != 100 || got.ParserVersion == "" {
		t.Fatalf("missing provenance: %s", w.Body.String())
	}
	// Each contract entry reuses the /v1/contracts summary shape exactly.
	var shape struct {
		Contracts []map[string]json.RawMessage `json:"contracts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"contract_id", "kind", "wasm_hash", "exec_ref", "archived"} {
		if _, ok := shape.Contracts[0][key]; !ok || len(shape.Contracts[0]) != 5 {
			t.Fatalf("summary shape drifted: %v", shape.Contracts[0])
		}
	}
	if w := serve(t, s, "/contracts?ids="+unknown); w.Code != 200 || !strings.Contains(w.Body.String(), `"contracts":[]`) || !strings.Contains(w.Body.String(), `"not_found":["`+unknown+`"]`) {
		t.Fatalf("alias or all-unknown lookup: %d %s", w.Code, w.Body.String())
	}
}

func TestBulkLookupRejectsInvalidInputBeforeQuerying(t *testing.T) {
	s := catalogServer(t)
	valid, unknown := catalogID(t, 1), catalogID(t, 99)
	many := make([]string, store.MaxLookupIDs+1)
	for i := range many {
		many[i] = valid
	}
	tests := []struct {
		name, query, message string
	}{
		{"mixed valid unknown and invalid fails as a whole", "ids=" + valid + "," + unknown + ",GABC", "ids entry 3 is not a contract ID"},
		{"account strkey is not a contract", "ids=GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7", "ids entry 1 is not a contract ID"},
		{"lowercase contract ID is rejected", "ids=" + strings.ToLower(valid), "ids entry 1 is not a contract ID"},
		{"empty entry from a trailing comma", "ids=" + valid + ",", "ids entry 2 is not a contract ID"},
		{"over the cap", "ids=" + strings.Join(many, ","), "at most 100 ids per request"},
		{"empty list", "ids=", ""},
		{"repeated parameter", "ids=" + valid + "&ids=" + valid, ""},
		{"combined with a filter", "ids=" + valid + "&implements=41", "ids cannot be combined with other parameters"},
		{"combined with pagination", "ids=" + valid + "&limit=5", "ids cannot be combined with other parameters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serve(t, s, "/v1/contracts?"+tt.query)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_request"`) || !strings.Contains(w.Body.String(), tt.message) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "GAAZI4") {
				t.Fatal("error echoed raw input")
			}
		})
	}
	// Exactly the cap is accepted.
	if w := serve(t, s, "/v1/contracts?ids="+strings.Join(many[:store.MaxLookupIDs], ",")); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBulkLookupChargesRateLimitPerTenIDs(t *testing.T) {
	s := catalogServer(t)
	s.options.RateLimit = 12
	ids := make([]string, 25)
	for i := range ids {
		ids[i] = catalogID(t, 1)
	}
	request := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.9:1"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	bulk := "/v1/contracts?ids=" + strings.Join(ids, ",")
	// 25 IDs cost 3. Four lookups use all 12; the fifth request is refused.
	for i := 0; i < 4; i++ {
		if c := request(bulk); c != 200 {
			t.Fatalf("lookup %d: %d", i, c)
		}
	}
	if c := request("/v1/stats"); c != 429 {
		t.Fatalf("budget not charged per ID block: %d", c)
	}
	for _, tt := range []struct {
		path string
		want int
	}{
		{"/v1/stats", 1},
		{"/v1/contracts?implements=41", 1},
		{"/v1/contracts?ids=a", 1},
		{"/v1/contracts?ids=" + strings.Repeat("a,", 10) + "a", 2},
		{"/v1/contracts?ids=" + strings.Repeat("a,", 99) + "a", 10},
		{"/v1/contracts?ids=" + strings.Repeat("a,", 500) + "a", 10},
	} {
		r := httptest.NewRequest("GET", tt.path, nil)
		if got := lookupCost(r.URL.Path, r.URL.Query()); got != tt.want {
			t.Fatalf("%s: cost %d, want %d", tt.path[:20], got, tt.want)
		}
	}
	// A cost above the whole budget is charged as the whole budget, so the
	// request is possible once per window rather than never.
	s.options.RateLimit = 5
	s.limiter = ipLimiter{}
	full := make([]string, 100)
	for i := range full {
		full[i] = catalogID(t, 1)
	}
	if c := request("/v1/contracts?ids=" + strings.Join(full, ",")); c != 200 {
		t.Fatalf("large lookup under small budget: %d", c)
	}
	if c := request("/v1/stats"); c != 429 {
		t.Fatalf("large lookup did not use the window: %d", c)
	}
}
