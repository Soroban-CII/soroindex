package store

import (
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
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
	page, err := s.Gap(ctx, 41, "sep41-v0.5.2", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	got := page.Contracts
	if len(got) != 2 || got[0].ContractID != "C1" || got[1].ContractID != "C6" || got[0].OKCount != 10 || page.NextCursor != nil {
		t.Fatalf("gap = %+v, want C1 and C6", got)
	}
	empty, err := s.Gap(ctx, 50, "sep50-v1.0.0", 0, "")
	if err != nil || empty.Contracts == nil || len(empty.Contracts) != 0 || empty.NextCursor != nil {
		t.Fatalf("gap for a SEP with no rules = %+v, %v; want empty, non-nil", empty, err)
	}
}

func TestGapPagination(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if err := exec(t, s, `INSERT INTO interface_matches VALUES ('legacy',41,'current','match',10,'[]','[]','{}','t')`); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 503)
	for i := range ids {
		raw := make([]byte, 32)
		binary.BigEndian.PutUint32(raw, uint32(i))
		id, err := strkey.Encode(strkey.VersionByteContract, raw)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		if err := exec(t, s, `INSERT INTO contracts (contract_id,kind,current_wasm_hash) VALUES (?,'wasm','legacy')`, id); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(ids)
	for _, limit := range []int{0, 1, 50, 500} {
		var got []string
		cursor := ""
		for {
			page, err := s.Gap(ctx, 41, "current", limit, cursor)
			bound := limit
			if bound == 0 {
				bound = 50
			}
			if err != nil || len(page.Contracts) > bound {
				t.Fatalf("limit %d: %+v, %v", limit, page, err)
			}
			for _, r := range page.Contracts {
				got = append(got, r.ContractID)
				if r.WasmHash != "legacy" || r.OKCount != 10 {
					t.Fatal(r)
				}
			}
			if page.NextCursor == nil {
				break
			}
			if *page.NextCursor == cursor || len(got) >= len(ids) {
				t.Fatal("cursor did not advance or unnecessary extra page", page)
			}
			cursor = *page.NextCursor
		}
		if strings.Join(got, ",") != strings.Join(ids, ",") {
			t.Fatalf("limit %d: got %d contracts, want %d without duplicates or omissions", limit, len(got), len(ids))
		}
	}
	last, err := s.Gap(ctx, 41, "current", 500, encodeContractCursor(ids[len(ids)-1]))
	if err != nil || last.Contracts == nil || len(last.Contracts) != 0 || last.NextCursor != nil {
		t.Fatal(last, err)
	}
}

func TestGapRejectsInvalidPaginationAndCancellation(t *testing.T) {
	s, _ := openTest(t)
	for _, input := range []struct {
		sep     int
		ruleset string
		limit   int
		cursor  string
	}{
		{41, "current", -1, ""}, {41, "current", 501, ""},
		{0, "current", 50, ""}, {1000000, "current", 50, ""}, {41, "", 50, ""},
		{41, "current", 50, "not-a-cursor"}, {41, "current", 50, strings.Repeat("a", 257)},
		{41, "current", 50, encodeContractCursor("C1")},
		{41, "current", 50, "eyJ2IjoyLCJpZCI6IkMxIn0"},
	} {
		if _, err := s.Gap(context.Background(), input.sep, input.ruleset, input.limit, input.cursor); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("%+v: %v", input, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Gap(ctx, 41, "current", 50, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestTotalsInstanceArchival(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO wasm (hash, parse_status, parser_version) VALUES ('w1','ok','1')`,
		`INSERT INTO wasm (hash, parse_status, parser_version) VALUES ('w2','archived','1')`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C1','wasm','w1')`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, archived) VALUES ('C2','wasm','w1',1)`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C3','wasm','w2')`,
	} {
		if err := exec(t, s, q); err != nil {
			t.Fatal(err)
		}
	}
	live, err := s.Totals(ctx, "r", false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.Totals(ctx, "r", true)
	if err != nil {
		t.Fatal(err)
	}
	// C3 runs archived code: never measured. C2's instance is archived:
	// measured only when instance archival is ignored.
	if live.LiveWasm != 2 || live.MeasuredContracts != 1 || all.LiveWasm != 3 || all.MeasuredContracts != 2 {
		t.Fatalf("live %+v, all %+v", live, all)
	}
}
