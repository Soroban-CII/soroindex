package api

import (
	"encoding/json"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestStatsAdoptionAndGap(t *testing.T) {
	s := catalogServer(t)
	w := serve(t, s, "/v1/stats")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d store.Statistics
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.TotalContracts != 4 || d.Adoption.LiveSAC != 1 || d.Adoption.MeasuredContracts != 3 || d.Adoption.DeclaringContracts != 2 || d.Adoption.GapContracts != 1 || d.LastLedger != 100 || d.Network != "testnet" || d.RulesetVersions[41] != "current" {
		t.Fatalf("%+v", d)
	}
	if w := serve(t, s, "/v1/stats?limit=1"); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
