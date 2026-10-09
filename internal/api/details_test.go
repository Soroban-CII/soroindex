package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestContractFixedShapeAndHistory(t *testing.T) {
	s := catalogServer(t)
	id := catalogID(t, 1)
	w := serve(t, s, "/v1/contracts/"+id)
	var d store.ContractDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || d.ContractID != id || d.Network != "testnet" || d.AsOfLedger != 100 || len(d.Claims) != 2 || !d.Claims[1].Declared || d.Claims[1].Inferred == nil || len(d.History) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"contract_id", "network", "kind", "wasm_hash", "exec_ref", "archived", "claims", "history", "parser_version", "as_of_ledger"} {
		if _, ok := shape[key]; !ok {
			t.Fatalf("missing fixed field %s", key)
		}
	}
	if len(shape) != 10 {
		t.Fatalf("unexpected fixed detail shape: %v", shape)
	}
	w = serve(t, s, "/v1/contracts/"+id+"/history")
	var history store.HistoryDetail
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(history.History) != 1 || len(history.History[0].Claims) != 2 {
		t.Fatal(w.Body.String())
	}
	w = serve(t, s, "/v1/contracts/"+catalogID(t, 4))
	if !strings.Contains(w.Body.String(), `"protocol":true`) || strings.Contains(w.Body.String(), `"declared":true`) {
		t.Fatal("SAC mixed into declared tier")
	}
	for _, path := range []string{"/v1/contracts/invalid", "/v1/contracts/invalid/history", "/v1/contracts/" + id + "?limit=1"} {
		if w := serve(t, s, path); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, suffix := range []string{"", "/history"} {
		if w := serve(t, s, "/v1/contracts/"+catalogID(t, 99)+suffix); w.Code != 404 {
			t.Fatalf("missing: %d", w.Code)
		}
	}
}
