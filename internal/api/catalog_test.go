package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

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
func catalogServer(t *testing.T) *Server {
	t.Helper()
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := byte(1); i <= 4; i++ {
			hash := fmt.Sprintf("%064x", i)
			kind := "wasm"
			if i == 4 {
				kind = "sac"
				hash = ""
			}
			id := catalogID(t, i)
			if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: kind, CurrentWasmHash: hash, UpdatedLedger: 100}); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.SetVersion(ctx, store.Version{ContractID: id, WasmHash: hash, FromLedger: 100}); err != nil {
				t.Fatal(err)
			}
			if hash != "" {
				if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: hash, HasMeta: true, HasSpec: true, MetaJSON: "[]", ParseStatus: "ok", ParserVersion: "1"}); err != nil {
					t.Fatal(err)
				}
				claims := []store.ClaimRow{{SEP: 41, RawToken: "41"}}
				if i == 1 {
					claims = append(claims, store.ClaimRow{SEP: 40, RawToken: "40"})
				}
				if i == 3 {
					claims = nil
				}
				if err := tx.ReplaceClaims(ctx, hash, "sep47-meta", claims); err != nil {
					t.Fatal(err)
				}
				if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: "current", Status: "match", OKCount: 10, MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-10-09T00:00:00Z"}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tx.SetLastLedger(ctx, 100); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 103}, Rulesets: map[int]string{41: "current"}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func catalogServerWithArchivedAndRef(t *testing.T) *Server {
	t.Helper()
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Contract 1: live wasm, declares SEP-41
		id1 := catalogID(t, 1)
		hash1 := fmt.Sprintf("%064x", 1)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id1, Kind: "wasm", CurrentWasmHash: hash1, UpdatedLedger: 100, Archived: false}); err != nil {
			t.Fatal(err)
		}
		// Contract 2: archived wasm, declares SEP-41
		id2 := catalogID(t, 2)
		hash2 := fmt.Sprintf("%064x", 2)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id2, Kind: "wasm", CurrentWasmHash: hash2, UpdatedLedger: 100, Archived: true}); err != nil {
			t.Fatal(err)
		}
		// Contract 3: live wasm_ref, declares SEP-41, references owner A
		id3 := catalogID(t, 3)
		hash3 := fmt.Sprintf("%064x", 3)
		ownerA := catalogID(t, 10)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id3, Kind: "wasm_ref", CurrentWasmHash: hash3, ExecRefOwner: ownerA, ExecRefTag: "v1", UpdatedLedger: 100, Archived: false}); err != nil {
			t.Fatal(err)
		}
		// Contract 4: live wasm_ref, declares SEP-41, references owner B
		id4 := catalogID(t, 4)
		hash4 := fmt.Sprintf("%064x", 4)
		ownerB := catalogID(t, 11)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id4, Kind: "wasm_ref", CurrentWasmHash: hash4, ExecRefOwner: ownerB, ExecRefTag: "v1", UpdatedLedger: 100, Archived: false}); err != nil {
			t.Fatal(err)
		}
		// Contract 5: archived wasm_ref, references owner A
		id5 := catalogID(t, 5)
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id5, Kind: "wasm_ref", CurrentWasmHash: hash3, ExecRefOwner: ownerA, ExecRefTag: "v1", UpdatedLedger: 100, Archived: true}); err != nil {
			t.Fatal(err)
		}
		// Wasm and claims for all
		for _, hash := range []string{hash1, hash2, hash3, hash4} {
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
	s, err := New(Options{Store: ro, Network: "testnet", RPC: tip{sequence: 103}, Rulesets: map[int]string{41: "current"}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestContractsFiltersPaginationAndAlias(t *testing.T) {
	s := catalogServer(t)
	cases := []struct {
		path  string
		count int
	}{{"/v1/contracts?implements=41", 2}, {"/v1/contracts?implements=41,40", 1}, {"/v1/contracts?implements_any=41,40", 2}, {"/v1/contracts?tier=inferred&implements=41", 3}, {"/v1/contracts?tier=protocol&implements=41", 1}, {"/v1/contracts?tier=declared&kind=sac", 0}, {"/contracts?implements=41", 2}, {"/v1/contracts?tier=inferred&implements=40", 0}}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := serve(t, s, tc.path)
			var page store.Page
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(page.Contracts) != tc.count {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	w := serve(t, s, "/v1/contracts?implements=41&limit=1")
	var first store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == nil {
		t.Fatal("missing continuation")
	}
	w = serve(t, s, "/v1/contracts?implements=41&limit=1&cursor="+url.QueryEscape(*first.NextCursor))
	var second store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Contracts) != 1 || second.Contracts[0].ID == first.Contracts[0].ID || second.NextCursor != nil {
		t.Fatal("pagination duplicated or lost rows")
	}
	for _, query := range []string{"limit=0", "limit=", "limit=501", "implements=0", "implements=41,", "tier=unknown", "kind=account", "cursor=bad", "limit=1&limit=2", "oops=1", "cursor=%ZZ"} {
		w := serve(t, s, "/v1/contracts?"+query)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
	w = serve(t, s, "/v1/contracts?tier=inferred&implements=41")
	var inferred store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &inferred); err != nil || inferred.RulesetVersions[41] != "current" || inferred.Tier != "inferred" {
		t.Fatal(w.Body.String())
	}
}

func TestContractsArchivedFilter(t *testing.T) {
	s := catalogServerWithArchivedAndRef(t)

	// archived=false (default) returns only live contracts
	w := serve(t, s, "/v1/contracts?implements=41&archived=false")
	var page store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 3 { // id1, id3, id4 are live
		t.Fatalf("expected 3 live contracts, got %d: %s", len(page.Contracts), w.Body.String())
	}
	for _, c := range page.Contracts {
		if c.Archived {
			t.Fatalf("contract %s should not be archived", c.ID)
		}
	}

	// archived=true returns only archived contracts
	w = serve(t, s, "/v1/contracts?implements=41&archived=true")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 2 { // id2, id5 are archived
		t.Fatalf("expected 2 archived contracts, got %d: %s", len(page.Contracts), w.Body.String())
	}
	for _, c := range page.Contracts {
		if !c.Archived {
			t.Fatalf("contract %s should be archived", c.ID)
		}
	}

	// omitted archived defaults to live only
	w = serve(t, s, "/v1/contracts?implements=41")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 3 {
		t.Fatalf("default should be live only, got %d", len(page.Contracts))
	}

	// invalid archived value
	w = serve(t, s, "/v1/contracts?implements=41&archived=yes")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("invalid archived: %d %s", w.Code, w.Body.String())
	}
}

func TestContractsExecRefOwnerFilter(t *testing.T) {
	s := catalogServerWithArchivedAndRef(t)
	ownerA := catalogID(t, 10)
	ownerB := catalogID(t, 11)

	// Filter by owner A (should return id3 and id5, but id5 is archived so only id3 by default)
	w := serve(t, s, "/v1/contracts?implements=41&exec_ref_owner="+ownerA)
	var page store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 1 || page.Contracts[0].ExecRef == nil || page.Contracts[0].ExecRef.Owner != ownerA {
		t.Fatalf("expected 1 contract with owner A, got %d: %s", len(page.Contracts), w.Body.String())
	}

	// Filter by owner B (should return id4)
	w = serve(t, s, "/v1/contracts?implements=41&exec_ref_owner="+ownerB)
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 1 || page.Contracts[0].ExecRef == nil || page.Contracts[0].ExecRef.Owner != ownerB {
		t.Fatalf("expected 1 contract with owner B, got %d: %s", len(page.Contracts), w.Body.String())
	}

	// Filter by owner A with archived=true (should return id5)
	w = serve(t, s, "/v1/contracts?implements=41&exec_ref_owner="+ownerA+"&archived=true")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 1 || page.Contracts[0].ExecRef == nil || page.Contracts[0].ExecRef.Owner != ownerA || !page.Contracts[0].Archived {
		t.Fatalf("expected 1 archived contract with owner A, got %d: %s", len(page.Contracts), w.Body.String())
	}

	// Invalid exec_ref_owner
	w = serve(t, s, "/v1/contracts?implements=41&exec_ref_owner=not-a-contract-id")
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("invalid exec_ref_owner: %d %s", w.Code, w.Body.String())
	}

	// exec_ref_owner combined with kind filter
	w = serve(t, s, "/v1/contracts?implements=41&exec_ref_owner="+ownerA+"&kind=wasm_ref")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(page.Contracts) != 1 || page.Contracts[0].Kind != "wasm_ref" {
		t.Fatalf("expected 1 wasm_ref with owner A, got %d: %s", len(page.Contracts), w.Body.String())
	}
}
