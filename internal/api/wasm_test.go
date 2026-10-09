package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestWasmEvidenceAndBadInput(t *testing.T) {
	s := catalogServer(t)
	hash := fmt.Sprintf("%064x", 1)
	w := serve(t, s, "/v1/wasm/"+hash+"?limit=1")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d store.WasmDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Claims) != 2 || len(d.Matches) != 1 || len(d.Contracts) != 1 || d.Contracts[0].ID != catalogID(t, 1) || d.ParserVersion != "1" || d.AsOfLedger != 100 {
		t.Fatalf("%+v", d)
	}
	for _, p := range []string{"/v1/wasm/abc", "/v1/wasm/" + hash + "?cursor=bad", "/v1/wasm/" + hash + "?limit=501", "/v1/wasm/" + hash + "?limit=", "/v1/wasm/" + hash + "?x=1"} {
		if w := serve(t, s, p); w.Code != 400 {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	if w := serve(t, s, "/v1/wasm/"+fmt.Sprintf("%064x", 99)); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if store.ValidateWasmHash("ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab") == nil {
		t.Fatal("uppercase accepted")
	}
}
