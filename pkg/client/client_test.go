package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/api"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
)

func catalogID(t *testing.T, n byte) string {
	t.Helper()
	var b [32]byte
	b[0] = n
	id, err := strkey.Encode(strkey.VersionByteContract, b[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type tip struct {
	sequence uint32
}

func (t tip) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: t.sequence}, nil
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id1 := catalogID(t, 1)
		hash1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id1, Kind: "wasm", CurrentWasmHash: hash1, UpdatedLedger: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: hash1, FromLedger: 100}); err != nil {
			t.Fatal(err)
		}
		id2 := catalogID(t, 2)
		hash2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id2, Kind: "wasm", CurrentWasmHash: hash2, UpdatedLedger: 100, Archived: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id2, WasmHash: hash2, FromLedger: 100}); err != nil {
			t.Fatal(err)
		}
		id3 := catalogID(t, 3)
		hash3 := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		owner := catalogID(t, 4)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id3, Kind: "wasm_ref", CurrentWasmHash: hash3, ExecRefOwner: owner, ExecRefTag: "v1", UpdatedLedger: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id3, WasmHash: hash3, ExecRefOwner: owner, ExecRefTag: "v1", FromLedger: 100}); err != nil {
			t.Fatal(err)
		}
		for _, hash := range []string{hash1, hash2, hash3} {
			if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: hash, HasMeta: true, HasSpec: true, MetaJSON: "[]", ParseStatus: "ok", ParserVersion: "1"}); err != nil {
				t.Fatal(err)
			}
			if err := tx.ReplaceClaims(ctx, hash, "sep47-meta", []store.ClaimRow{{SEP: 41, RawToken: "41"}}); err != nil {
				t.Fatal(err)
			}
			if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: "current", Status: "match", OKCount: 10, MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-10-09T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.SetLastLedger(ctx, 100); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := api.New(api.Options{Store: ro, Network: "testnet", RPC: tip{sequence: 103}, Rulesets: map[int]string{41: "current"}})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(s)
}

func apiStore(t *testing.T, prepare func(*store.Store)) *store.Store {
	t.Helper()
	ctx := context.Background()
	path := t.TempDir() + "/api.db"
	w, err := store.Open(ctx, path, store.Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(w)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := store.Open(ctx, path, store.Options{Passphrase: "test", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

func boolPtr(b bool) *bool {
	return &b
}

func TestClientContracts(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	page, err := c.Contracts(context.Background(), ContractsFilter{
		Implements: []int{41},
		Tier:       "declared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Contracts) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(page.Contracts))
	}
	for _, con := range page.Contracts {
		if con.Archived {
			t.Fatalf("contract %s should not be archived", con.ID)
		}
	}

	page, err = c.Contracts(context.Background(), ContractsFilter{
		Implements: []int{41},
		Tier:       "inferred",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Contracts) != 2 {
		t.Fatalf("expected 2 inferred contracts, got %d", len(page.Contracts))
	}

	page, err = c.Contracts(context.Background(), ContractsFilter{
		Implements: []int{41},
		Archived:   boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Contracts) != 1 {
		t.Fatalf("expected 1 archived contract, got %d", len(page.Contracts))
	}
	if !page.Contracts[0].Archived {
		t.Fatalf("contract should be archived")
	}

	owner := catalogID(t, 4)
	page, err = c.Contracts(context.Background(), ContractsFilter{
		Implements:   []int{41},
		ExecRefOwner: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Contracts) != 1 {
		t.Fatalf("expected 1 contract with owner, got %d", len(page.Contracts))
	}
	if page.Contracts[0].ExecRef == nil || page.Contracts[0].ExecRef.Owner != owner {
		t.Fatalf("wrong exec ref: %+v", page.Contracts[0].ExecRef)
	}

	it := c.ContractsIterator(context.Background(), ContractsFilter{
		Tier:  "declared",
		Limit: 1,
	})
	count := 0
	for it.Next() {
		count += len(it.Contracts())
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("iterator should return 2 contracts, got %d", count)
	}
}

func TestClientLookupContracts(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	id1 := catalogID(t, 1)
	id2 := catalogID(t, 2)
	id3 := catalogID(t, 3)
	notFoundID := catalogID(t, 255)

	ids := []string{
		id1,
		id2,
		id3,
		notFoundID,
	}
	lookup, err := c.LookupContracts(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(lookup.Contracts) != 3 {
		t.Fatalf("expected 3 found, got %d", len(lookup.Contracts))
	}
	if len(lookup.NotFound) != 1 {
		t.Fatalf("expected 1 not found, got %d", len(lookup.NotFound))
	}
	if lookup.NotFound[0] != notFoundID {
		t.Fatalf("wrong not found ID: %s", lookup.NotFound[0])
	}
}

func TestClientGetContract(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	id1 := catalogID(t, 1)
	detail, err := c.GetContract(context.Background(), id1)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ContractID != id1 {
		t.Fatalf("wrong contract ID: %s", detail.ContractID)
	}
	if len(detail.Claims) != 1 || detail.Claims[0].SEP != 41 {
		t.Fatalf("wrong claims: %+v", detail.Claims)
	}
	if detail.Kind != "wasm" {
		t.Fatalf("wrong kind: %s", detail.Kind)
	}

	notFoundID := catalogID(t, 255)
	_, err = c.GetContract(context.Background(), notFoundID)
	if err == nil {
		t.Fatal("expected error for not found")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		t.Fatalf("expected not_found error, got %v", err)
	}
}

func TestClientGetHistory(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	id1 := catalogID(t, 1)
	page, err := c.GetHistory(context.Background(), id1, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.History) != 1 {
		t.Fatalf("expected 1 history version, got %d", len(page.History))
	}

	it := c.HistoryIterator(context.Background(), id1, 1)
	count := 0
	for it.Next() {
		count += len(it.History())
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("iterator should return 1 version, got %d", count)
	}
}

func TestClientGetWasm(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	detail, err := c.GetWasm(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WasmFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("wrong hash: %s", detail.Hash)
	}
	if len(detail.Claims) != 1 || detail.Claims[0].SEP != 41 {
		t.Fatalf("wrong claims: %+v", detail.Claims)
	}
	if len(detail.Matches) != 1 || detail.Matches[0].SEP != 41 {
		t.Fatalf("wrong matches: %+v", detail.Matches)
	}
	if len(detail.Contracts) != 1 {
		t.Fatalf("expected 1 contract, got %d", len(detail.Contracts))
	}

	_, err = c.GetWasm(context.Background(), "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", WasmFilter{})
	if err == nil {
		t.Fatal("expected error for not found")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "not_found" {
		t.Fatalf("expected not_found error, got %v", err)
	}
}

func TestClientGetSEPs(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	page, err := c.GetSEPs(context.Background(), SEPsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.SEPs) != 1 || page.SEPs[0].SEP != 41 {
		t.Fatalf("wrong SEPs: %+v", page.SEPs)
	}
	if page.SEPs[0].Declared != 2 || page.SEPs[0].Inferred != 2 {
		t.Fatalf("wrong counts: %+v", page.SEPs[0])
	}

	it := c.SEPsIterator(context.Background(), SEPsFilter{Limit: 1})
	count := 0
	for it.Next() {
		count += len(it.SEPs())
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("iterator should return 1 SEP, got %d", count)
	}
}

func TestClientGetStats(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	stats, err := c.GetStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Network != "testnet" {
		t.Fatalf("wrong network: %s", stats.Network)
	}
	if stats.TotalContracts != 3 {
		t.Fatalf("wrong total: %d", stats.TotalContracts)
	}
}

func TestClientGetToolchains(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	stats, err := c.GetToolchains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Network != "testnet" {
		t.Fatalf("wrong network: %s", stats.Network)
	}
}

func TestClientGetHealth(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	health, err := c.GetHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" {
		t.Fatalf("wrong status: %s", health.Status)
	}
	if health.Network != "testnet" {
		t.Fatalf("wrong network: %s", health.Network)
	}
	if health.LatestLedger != 103 {
		t.Fatalf("wrong latest ledger: %d", health.LatestLedger)
	}
}

func TestClientUpgradesStream(t *testing.T) {
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id1 := catalogID(t, 1)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id1, Kind: "wasm", CurrentWasmHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UpdatedLedger: 20}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: "0000000000000000000000000000000000000000000000000000000000000001", FromLedger: 10}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.SetVersion(ctx, store.Version{ContractID: id1, WasmHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", FromLedger: 20}); err != nil {
			t.Fatal(err)
		}
		if err := tx.SetLastLedger(ctx, 20); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := api.New(api.Options{Store: ro, Network: "testnet", RPC: tip{sequence: 25}, Rulesets: map[int]string{41: "current"}})
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), api.TestTickerIntervalKey, 10*time.Millisecond)
		s.ServeHTTP(w, r.WithContext(ctx))
	})
	go func() {
		server := &http.Server{Handler: testHandler, ReadHeaderTimeout: 5 * time.Second}
		_ = server.Serve(listener)
	}()

	serverURL := "http://" + listener.Addr().String()

	c := NewClient(serverURL, WithTimeout(5*time.Second))

	var changes []VersionChange
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = c.UpgradesStream(ctx, 0, func(change VersionChange) error {
		changes = append(changes, change)
		if len(changes) >= 2 {
			cancel()
		}
		return nil
	})
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if len(changes) < 2 {
		t.Fatalf("expected at least 2 changes, got %d: %+v", len(changes), changes)
	}
}

func TestClientErrors(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	c := NewClient(srv.URL, WithTimeout(5*time.Second))

	_, err := c.Contracts(context.Background(), ContractsFilter{
		Tier: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid tier")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "bad_request" {
		t.Fatalf("expected bad_request error, got %v", err)
	}
}

func TestAPIErrorAs(t *testing.T) {
	err := &APIError{Code: "not_found", Message: "contract not found"}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed")
	}
	if apiErr.Code != "not_found" {
		t.Fatal("wrong code")
	}
}
