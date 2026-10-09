package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestLatestFailedVerificationCannotMatchOlderPass(t *testing.T) {
	ro := apiStore(t, func(w *store.Store) {
		ctx := context.Background()
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := byte(1); i <= 2; i++ {
			hash := fmt.Sprintf("%064x", i)
			if err = tx.UpsertContract(ctx, store.ContractRow{ID: catalogID(t, i), Kind: "wasm", CurrentWasmHash: hash}); err != nil {
				t.Fatal(err)
			}
			if i == 1 {
				if err = tx.ReplaceClaims(ctx, hash, "sep47-meta", []store.ClaimRow{{SEP: 41, RawToken: "41"}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct {
			id                byte
			tool, verdict, at string
			passed            int
		}{{1, "old-suite", "pass", "2026-10-09T00:00:00Z", 1}, {1, "new-suite", "fail", "2026-10-09T01:00:00Z", 0}, {2, "suite", "pass", "2026-10-09T00:00:00Z", 1}} {
			if _, err = w.DB.ExecContext(ctx, `INSERT INTO verifications VALUES (?,?,41,?,'1',?,?,'[]',?)`, catalogID(t, v.id), fmt.Sprintf("%064x", v.id), v.tool, v.verdict, v.passed, v.at); err != nil {
				t.Fatal(err)
			}
		}
	})
	s, err := New(Options{Store: ro, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, s, "/v1/contracts?tier=verified&implements=41")
	var p store.Page
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 || len(p.Contracts) != 0 {
		t.Fatal(w.Body.String())
	}
	w = serve(t, s, "/v1/seps")
	var counts store.SEPPage
	if err = json.Unmarshal(w.Body.Bytes(), &counts); err != nil || counts.SEPs[0].Verified != 0 {
		t.Fatal(w.Body.String())
	}
}
