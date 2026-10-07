package store

import (
	"context"
	"testing"
)

func TestGap(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, q := range []string{
		// legacy: matches, declares nothing -> in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C1','wasm','legacy')`,
		`INSERT INTO interface_matches VALUES ('legacy',41,'sep41-v0.5.2','match',10,'[]','[]','{}','t')`,
		// declared: matches but declares 41 -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C2','wasm','declared')`,
		`INSERT INTO interface_matches VALUES ('declared',41,'sep41-v0.5.2','match',10,'[]','[]','{}','t')`,
		`INSERT INTO wasm_claims VALUES ('declared',41,'sep47-meta','41',NULL)`,
		// declares some other SEP -> declares something -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C3','wasm','other')`,
		`INSERT INTO interface_matches VALUES ('other',41,'sep41-v0.5.2','match',10,'[]','[]','{}','t')`,
		`INSERT INTO wasm_claims VALUES ('other',40,'sep47-meta','40',NULL)`,
		// partial -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C4','wasm','partial')`,
		`INSERT INTO interface_matches VALUES ('partial',41,'sep41-v0.5.2','partial',8,'[]','[]','{}','t')`,
		// archived contract on a gap hash -> excluded
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, archived) VALUES ('C5','wasm','legacy',1)`,
		// resolved reference on a gap hash -> included
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, exec_ref_owner, exec_ref_tag) VALUES ('C6','wasm_ref','legacy','C9','v1')`,
		// a match under an older ruleset only -> not in the current gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C7','wasm','stale')`,
		`INSERT INTO interface_matches VALUES ('stale',41,'sep41-v0.4.0','match',10,'[]','[]','{}','t')`,
		// a SAC implements SEP-41 by protocol, never in the inferred gap
		`INSERT INTO contracts (contract_id, kind) VALUES ('C8','sac')`,
	} {
		if err := exec(t, s, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	got, err := s.Gap(ctx, 41, "sep41-v0.5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ContractID != "C1" || got[1].ContractID != "C6" || got[0].OKCount != 10 {
		t.Fatalf("gap = %+v, want C1 and C6", got)
	}
	empty, err := s.Gap(ctx, 50, "sep50-v1.0.0")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("gap for a SEP with no rules = %+v, %v; want empty, non-nil", empty, err)
	}
}
