package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestHistoryPaginationAndStaleVerification(t *testing.T) {
	id := catalogID(t, 1)
	oldHash := fmt.Sprintf("%064x", 1)
	newHash := fmt.Sprintf("%064x", 2)
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: newHash, UpdatedLedger: 20}); err != nil {
			t.Fatal(err)
		}
		for i, hash := range []string{oldHash, newHash} {
			if _, err = tx.SetVersion(ctx, store.Version{ContractID: id, WasmHash: hash, FromLedger: uint32(10 + i*10)}); err != nil {
				t.Fatal(err)
			}
			if err = tx.ReplaceClaims(ctx, hash, "sep47-meta", []store.ClaimRow{{SEP: 41, RawToken: "41"}}); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.SetLastLedger(ctx, 20); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err = w.DB.ExecContext(ctx, `INSERT INTO verifications (contract_id,wasm_hash,sep,tool,tool_version,verdict,passed,clauses_json,run_at) VALUES (?,?,41,'suite','1','pass',1,'[]','2026-10-09T00:00:00Z')`, id, oldHash); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, s, "/v1/contracts/"+id+"/history?limit=1")
	var first store.HistoryDetail
	if err = json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(first.History) != 1 || first.NextCursor == nil || first.History[0].Claims[0].Verified == nil || !first.History[0].Claims[0].Verified.Stale || *first.History[0].ToLedger != 20 {
		t.Fatal(w.Body.String())
	}
	w = serve(t, s, "/v1/contracts/"+id+"/history?limit=1&cursor="+url.QueryEscape(*first.NextCursor))
	var second store.HistoryDetail
	if err = json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.History) != 1 || second.NextCursor != nil || second.History[0].FromLedger != 20 || second.History[0].Claims[0].Verified != nil {
		t.Fatal(w.Body.String())
	}
	w = serve(t, s, "/v1/contracts?tier=verified&implements=41")
	var page store.Page
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Contracts) != 0 {
		t.Fatal("old result matched current filter", w.Body.String())
	}
	if w := serve(t, s, "/v1/contracts/"+id+"/history?cursor=bad"); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
