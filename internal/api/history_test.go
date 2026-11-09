package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
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

// TestHistorySeveralUpgrades covers a contract upgraded three times, with a
// switch to a CAP-85 reference in the middle. Every version is listed in
// from_ledger order, each closed at the next version's start, the current one
// open, and each carries the claims of its own Wasm.
func TestHistorySeveralUpgrades(t *testing.T) {
	id := catalogID(t, 1)
	owner := catalogID(t, 2)
	type step struct {
		hash, ref string
		from      uint32
		claim     string // raw sep token declared by this version's Wasm, "" for none
	}
	steps := []step{
		{fmt.Sprintf("%064x", 1), "", 10, "41"},
		{fmt.Sprintf("%064x", 2), "", 20, ""},
		{fmt.Sprintf("%064x", 3), "blue", 30, "41,40"},
		{fmt.Sprintf("%064x", 4), "", 40, "40"},
	}
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range steps {
			v := store.Version{ContractID: id, WasmHash: st.hash, FromLedger: st.from}
			if st.ref != "" {
				v.ExecRefOwner, v.ExecRefTag = owner, st.ref
			}
			if _, err = tx.SetVersion(ctx, v); err != nil {
				t.Fatal(err)
			}
			var claims []store.ClaimRow
			if st.claim == "41" || st.claim == "41,40" {
				claims = append(claims, store.ClaimRow{SEP: 41, RawToken: "41"})
			}
			if st.claim == "40" || st.claim == "41,40" {
				claims = append(claims, store.ClaimRow{SEP: 40, RawToken: "40"})
			}
			if err = tx.ReplaceClaims(ctx, st.hash, "sep47-meta", claims); err != nil {
				t.Fatal(err)
			}
		}
		last := steps[len(steps)-1]
		if err = tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "wasm", CurrentWasmHash: last.hash, UpdatedLedger: last.from}); err != nil {
			t.Fatal(err)
		}
		if err = tx.SetLastLedger(ctx, 45); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, s, "/v1/contracts/"+id+"/history")
	var d store.HistoryDetail
	if err = json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(d.History) != len(steps) || d.NextCursor != nil {
		t.Fatalf("want %d versions on one page: %s", len(steps), w.Body.String())
	}
	for i, st := range steps {
		v := d.History[i]
		if v.FromLedger != st.from || v.WasmHash == nil || *v.WasmHash != st.hash {
			t.Fatalf("version %d: %s", i, w.Body.String())
		}
		if i < len(steps)-1 {
			if v.ToLedger == nil || *v.ToLedger != steps[i+1].from {
				t.Fatalf("version %d not closed at %d: %s", i, steps[i+1].from, w.Body.String())
			}
		} else if v.ToLedger != nil {
			t.Fatalf("current version closed: %s", w.Body.String())
		}
		if (st.ref != "") != (v.ExecRef != nil) || (v.ExecRef != nil && (v.ExecRef.Owner != owner || v.ExecRef.Tag != st.ref)) {
			t.Fatalf("version %d exec_ref: %s", i, w.Body.String())
		}
		var seps []int
		for _, c := range v.Claims {
			if c.Declared {
				seps = append(seps, c.SEP)
			}
		}
		want := map[string]string{"": "[]", "41": "[41]", "41,40": "[40 41]", "40": "[40]"}[st.claim]
		sort.Ints(seps)
		if got := fmt.Sprint(seps); got != want {
			t.Fatalf("version %d declared %s, want %s: %s", i, got, want, w.Body.String())
		}
	}
}
