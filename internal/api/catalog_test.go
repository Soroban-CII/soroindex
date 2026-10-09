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
	for _, query := range []string{"limit=0", "limit=501", "implements=0", "implements=41,", "tier=unknown", "kind=account", "cursor=bad", "limit=1&limit=2", "oops=1"} {
		w := serve(t, s, "/v1/contracts?"+query)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
}
