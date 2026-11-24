package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func getWith(t *testing.T, s *Server, path, inm string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if inm != "" {
		r.Header.Set("If-None-Match", inm)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestETagOnEveryIndexEndpoint(t *testing.T) {
	s := catalogServer(t)
	id := catalogID(t, 1)
	hash := strings.Repeat("0", 63) + "1"
	paths := []string{
		"/v1/contracts", "/contracts", "/v1/contracts?implements=41&limit=2",
		"/v1/contracts?ids=" + id,
		"/v1/contracts/" + id, "/v1/contracts/" + id + "/history",
		"/v1/wasm/" + hash, "/v1/seps", "/v1/stats", "/v1/stats/toolchains",
	}
	for _, p := range paths {
		t.Run(p+" returns a strong tag and 304 on a match", func(t *testing.T) {
			w := getWith(t, s, p, "")
			etag := w.Header().Get("ETag")
			if w.Code != 200 || len(etag) < 3 || etag[0] != '"' || strings.HasPrefix(etag, "W/") {
				t.Fatalf("%d etag=%q", w.Code, etag)
			}
			n := getWith(t, s, p, etag)
			if n.Code != 304 || n.Body.Len() != 0 || n.Header().Get("ETag") != etag {
				t.Fatalf("%d body=%d etag=%q", n.Code, n.Body.Len(), n.Header().Get("ETag"))
			}
			if again := getWith(t, s, p, ""); again.Header().Get("ETag") != etag || again.Body.String() != w.Body.String() {
				t.Fatal("tag or body not stable between requests")
			}
		})
	}
}

func TestETagRejectsStaleWeakAndInvalidHeaders(t *testing.T) {
	s := catalogServer(t)
	etag := getWith(t, s, "/v1/stats", "").Header().Get("ETag")
	for name, header := range map[string]string{
		"stale tag":     `"0123456789abcdef0123456789abcdef"`,
		"unquoted":      strings.Trim(etag, `"`),
		"empty quotes":  `""`,
		"garbage":       `W/`,
		"other and bad": `"x", ,nonsense`,
	} {
		t.Run(name+" gets a full response", func(t *testing.T) {
			if w := getWith(t, s, "/v1/stats", header); w.Code != 200 || w.Body.Len() == 0 {
				t.Fatalf("%d", w.Code)
			}
		})
	}
	for name, header := range map[string]string{
		"weak form of the tag": "W/" + etag,
		"tag in a list":        `"x", ` + etag,
		"wildcard":             "*",
	} {
		t.Run(name+" matches by weak comparison", func(t *testing.T) {
			if w := getWith(t, s, "/v1/stats", header); w.Code != 304 {
				t.Fatalf("%d", w.Code)
			}
		})
	}
}

func TestETagSkipsErrorsHealthAndStream(t *testing.T) {
	s := catalogServer(t)
	etag := getWith(t, s, "/v1/stats", "").Header().Get("ETag")
	for _, p := range []string{"/v1/stats?limit=1", "/v1/contracts/invalid", "/v1/contracts/" + catalogID(t, 99), "/v1/nothing"} {
		t.Run(p+" has no tag", func(t *testing.T) {
			w := getWith(t, s, p, etag)
			if w.Code < 400 || w.Header().Get("ETag") != "" {
				t.Fatalf("%d etag=%q", w.Code, w.Header().Get("ETag"))
			}
		})
	}
	if h := getWith(t, s, "/v1/health", "*"); h.Header().Get("ETag") != "" || h.Code == 304 {
		t.Fatalf("health tagged: %d", h.Code)
	}
}

func TestETagChangesWithLedgerQueryParserAndRulesets(t *testing.T) {
	rules := map[int]string{41: "a", 40: "b"}
	base := responseETag("testnet", "/v1/stats", "", 100, "1", rules)
	for name, got := range map[string]string{
		"ledger":  responseETag("testnet", "/v1/stats", "", 101, "1", rules),
		"parser":  responseETag("testnet", "/v1/stats", "", 100, "2", rules),
		"ruleset": responseETag("testnet", "/v1/stats", "", 100, "1", map[int]string{41: "c", 40: "b"}),
		"query":   responseETag("testnet", "/v1/stats", "limit=1", 100, "1", rules),
		"network": responseETag("mainnet", "/v1/stats", "", 100, "1", rules),
		"path":    responseETag("testnet", "/v1/seps", "", 100, "1", rules),
	} {
		if got == base {
			t.Fatalf("tag did not change with %s", name)
		}
	}
	if responseETag("testnet", "/v1/stats", "", 100, "1", map[int]string{40: "b", 41: "a"}) != base {
		t.Fatal("tag depends on map order")
	}
}

func TestETagFollowsStoredLedger(t *testing.T) {
	at := func(ledger string) string {
		ro := apiStore(t, func(w *store.Store) {
			if err := w.SetState(context.Background(), store.KeyLastLedger, ledger); err != nil {
				t.Fatal(err)
			}
		})
		s, err := New(Options{Store: ro, Network: "testnet", Rulesets: map[int]string{41: "a"}})
		if err != nil {
			t.Fatal(err)
		}
		return getWith(t, s, "/v1/seps", "").Header().Get("ETag")
	}
	a, b := at("100"), at("101")
	if a == "" || a == b || a != at("100") {
		t.Fatalf("%q %q", a, b)
	}
}
