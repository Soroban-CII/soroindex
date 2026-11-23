package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func TestMultiNetworkSelectsBackend(t *testing.T) {
	testnet := apiStore(t, func(w *store.Store) {
		if err := w.SetState(context.Background(), store.KeyLastLedger, "100"); err != nil {
			t.Fatal(err)
		}
	})
	mainnet := apiStore(t, func(w *store.Store) {
		if err := w.SetState(context.Background(), store.KeyLastLedger, "200"); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Networks: map[string]Backend{
		"testnet": {Store: testnet, Network: "testnet", RPC: tip{sequence: 103}},
		"mainnet": {Store: mainnet, Network: "mainnet", RPC: tip{sequence: 205}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	w := serve(t, s, "/v1/health")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing network = %d %s", w.Code, w.Body.String())
	}
	for network, want := range map[string]uint32{"testnet": 100, "mainnet": 200} {
		w := serve(t, s, "/v1/health?network="+network)
		var h healthResponse
		if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || h.Network != network || h.LastLedger != want {
			t.Fatalf("%s = %d %+v", network, w.Code, h)
		}
	}
	if w := serve(t, s, "/v1/stats?network=mainnet"); w.Code != 200 {
		t.Fatalf("stats = %d %s", w.Code, w.Body.String())
	}
	if w := serve(t, s, "/v1/contracts?network=testnet&implements=41"); w.Code != 200 {
		t.Fatalf("contracts = %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{
		"/v1/health?network=devnet",
		"/v1/health?network=testnet&network=mainnet",
		"/v1/health?network=",
		"/v1/stats?network=unknown",
		"/v1/stats?network=testnet&extra=1",
	} {
		if w := serve(t, s, path); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
	}
	// The network parameter is stripped before a handler that normally
	// rejects all query parameters sees it.
	id, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(t, s, "/v1/contracts/"+id+"?network=testnet"); w.Code != http.StatusNotFound {
		t.Fatalf("contract = %d %s", w.Code, w.Body.String())
	}
}

func TestSingleNetworkAcceptsOnlyItsName(t *testing.T) {
	ro := apiStore(t, nil)
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(t, s, "/v1/health?network=testnet"); w.Code != 200 {
		t.Fatalf("same network = %d %s", w.Code, w.Body.String())
	}
	if w := serve(t, s, "/v1/health?network=mainnet"); w.Code != 400 {
		t.Fatalf("other network = %d %s", w.Code, w.Body.String())
	}
}

func TestNewRejectsInvalidBackends(t *testing.T) {
	ro := apiStore(t, nil)
	if _, err := New(Options{Networks: map[string]Backend{
		"testnet": {Store: ro, Network: "mainnet"},
	}}); err == nil {
		t.Fatal("mismatched network accepted")
	}
	if _, err := New(Options{Networks: map[string]Backend{
		"devnet": {Store: ro, Network: "devnet"},
	}}); err == nil {
		t.Fatal("unknown network accepted")
	}
	if _, err := New(Options{Networks: map[string]Backend{
		"testnet": {Store: nil, Network: "testnet"},
	}}); err == nil {
		t.Fatal("missing store accepted")
	}
}
