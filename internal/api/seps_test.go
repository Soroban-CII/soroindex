package api

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestSEPsTierCountsAndPagination(t *testing.T) {
	s := catalogServer(t)
	w := serve(t, s, "/v1/seps?limit=1")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d store.SEPPage
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.SEPs) != 1 || d.SEPs[0].SEP != 40 || d.SEPs[0].Declared != 1 || d.NextCursor == nil {
		t.Fatalf("%+v", d)
	}
	w = serve(t, s, "/v1/seps?limit=1&cursor="+url.QueryEscape(*d.NextCursor))
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.SEPs) != 1 || d.SEPs[0] != (store.SEPCounts{SEP: 41, Declared: 2, Inferred: 3, Protocol: 1}) || d.NextCursor != nil || d.RulesetVersions[41] != "current" {
		t.Fatalf("%+v", d)
	}
	for _, p := range []string{"/v1/seps?limit=0", "/v1/seps?cursor=bad", "/v1/seps?tier=declared", "/v1/seps?limit=1&limit=2"} {
		if w := serve(t, s, p); w.Code != 400 {
			t.Fatal(p, w.Code)
		}
	}
}
