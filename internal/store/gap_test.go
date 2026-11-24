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
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('legacy',41,'sep41-v0.5.2','unknown','match',10,'[]','[]','{}','t')`,
		// declared: matches but declares 41 -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C2','wasm','declared')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('declared',41,'sep41-v0.5.2','unknown','match',10,'[]','[]','{}','t')`,
		`INSERT INTO wasm_claims VALUES ('declared',41,'sep47-meta','41',NULL)`,
		// declares some other SEP -> declares something -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C3','wasm','other')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('other',41,'sep41-v0.5.2','unknown','match',10,'[]','[]','{}','t')`,
		`INSERT INTO wasm_claims VALUES ('other',40,'sep47-meta','40',NULL)`,
		// partial -> not in the gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C4','wasm','partial')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('partial',41,'sep41-v0.5.2','unknown','partial',8,'[]','[]','{}','t')`,
		// archived contract on a gap hash -> excluded
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, archived) VALUES ('C5','wasm','legacy',1)`,
		// resolved reference on a gap hash -> included
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, exec_ref_owner, exec_ref_tag) VALUES ('C6','wasm_ref','legacy','C9','v1')`,
		// a match under an older ruleset only -> not in the current gap
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C7','wasm','stale')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('stale',41,'sep41-v0.4.0','unknown','match',10,'[]','[]','{}','t')`,
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
	if err := exec(t, s, `INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('legacy',41,'current','unknown','match',10,'[]','[]','{}','t')`); err != nil {
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

func TestGapGroups(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, q := range []string{
		// Two contracts share factory hash F; one contract runs hash G.
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C1','wasm','F')`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, exec_ref_owner, exec_ref_tag) VALUES ('C2','wasm_ref','F','C90','v1')`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C3','wasm','G')`,
		// Archived contract on F and archived code on H are both excluded.
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash, archived) VALUES ('C4','wasm','F',1)`,
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C5','wasm','H')`,
		`INSERT INTO wasm (hash, parse_status, parser_version) VALUES ('H','archived','1')`,
		// Declares 41: not in the gap.
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C6','wasm','D')`,
		// Partial: not in the gap.
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C7','wasm','P')`,
		// Match only under an older ruleset: not in the current gap.
		`INSERT INTO contracts (contract_id, kind, current_wasm_hash) VALUES ('C8','wasm','S')`,
		// SAC: protocol tier, never the inferred gap.
		`INSERT INTO contracts (contract_id, kind) VALUES ('C9','sac')`,
	} {
		if err := exec(t, s, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('F',41,'cur','x','match',10,'[]','[]','{}','t')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('G',41,'cur','x','match',9,'[]','[]','{}','t')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('D',41,'cur','x','match',10,'[]','[]','{}','t')`,
		`INSERT INTO wasm_claims VALUES ('D',41,'sep47-meta','41',NULL)`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('P',41,'cur','x','partial',8,'[]','[]','{}','t')`,
		`INSERT INTO interface_matches (wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count, missing_json, mismatched_json, matched_variants_json, computed_at) VALUES ('S',41,'old','x','match',10,'[]','[]','{}','t')`,
	} {
		if err := exec(t, s, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	groups, totalGroups, totalContracts, err := s.GapGroups(ctx, 41, "cur", 10)
	if err != nil {
		t.Fatal(err)
	}
	if totalGroups != 2 || totalContracts != 3 || len(groups) != 2 {
		t.Fatalf("groups = %+v, totals %d/%d", groups, totalGroups, totalContracts)
	}
	if groups[0].WasmHash != "F" || len(groups[0].Contracts) != 2 || groups[0].Contracts[0] != "C1" || groups[0].Contracts[1] != "C2" || groups[0].OKCount != 10 {
		t.Fatalf("largest group = %+v", groups[0])
	}
	if groups[1].WasmHash != "G" || len(groups[1].Contracts) != 1 || groups[1].OKCount != 9 {
		t.Fatalf("second group = %+v", groups[1])
	}
	capped, tg, tc, err := s.GapGroups(ctx, 41, "cur", 1)
	if err != nil || len(capped) != 1 || capped[0].WasmHash != "F" || tg != 2 || tc != 3 {
		t.Fatalf("cap: %+v, %d/%d, %v", capped, tg, tc, err)
	}
	for _, bad := range []struct {
		sep, max int
		ruleset  string
	}{{0, 10, "cur"}, {1000000, 10, "cur"}, {41, 0, "cur"}, {41, 501, "cur"}, {41, 10, ""}} {
		if _, _, _, err := s.GapGroups(ctx, bad.sep, bad.ruleset, bad.max); !errors.Is(err, ErrBadQuery) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := s.GapGroups(ctx, 41, "cur", 10); !errors.Is(err, context.Canceled) {
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
