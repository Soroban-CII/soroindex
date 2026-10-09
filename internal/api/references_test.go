package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestWasmUserPaginationAndExecutableReferences(t *testing.T) {
	hash := fmt.Sprintf("%064x", 1)
	owner := catalogID(t, 99)
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.UpsertWasm(ctx, store.WasmRow{Hash: hash, ParseStatus: "ok", ParserVersion: "1", MetaJSON: "[]"}); err != nil {
			t.Fatal(err)
		}
		for i := byte(1); i <= 4; i++ {
			c := store.ContractRow{ID: catalogID(t, i), Kind: "wasm_ref", CurrentWasmHash: hash, ExecRefOwner: owner, ExecRefTag: "blue", Archived: i == 3}
			if i == 4 {
				c.CurrentWasmHash = ""
			}
			if err = tx.UpsertContract(ctx, c); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.SetVersion(ctx, store.Version{ContractID: c.ID, WasmHash: c.CurrentWasmHash, ExecRefOwner: owner, ExecRefTag: "blue", FromLedger: 10}); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, s, "/v1/wasm/"+hash+"?limit=1")
	var first store.WasmDetail
	if err = json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 200 || len(first.Contracts) != 1 || first.NextCursor == nil || first.Contracts[0].ExecRef.Owner != owner {
		t.Fatal(w.Body.String())
	}
	w = serve(t, s, "/v1/wasm/"+hash+"?limit=1&cursor="+url.QueryEscape(*first.NextCursor))
	var second store.WasmDetail
	if err = json.Unmarshal(w.Body.Bytes(), &second); err != nil || len(second.Contracts) != 1 || second.NextCursor != nil || second.Contracts[0].ID == first.Contracts[0].ID || second.Contracts[0].Archived {
		t.Fatal(w.Body.String())
	}
	for _, i := range []byte{3, 4} {
		w = serve(t, s, "/v1/contracts/"+catalogID(t, i))
		var d store.ContractDetail
		if err = json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 || d.ExecRef.Owner != owner || d.History[0].ExecRef.Tag != "blue" {
			t.Fatal(w.Body.String())
		}
		if i == 3 && !d.Archived {
			t.Fatal("archival lost")
		}
		if i == 4 && (d.WasmHash != nil || len(d.Claims) != 0) {
			t.Fatal("unresolved reference fabricated claims")
		}
	}
}
