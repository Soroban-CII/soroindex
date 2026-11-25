package ingest

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// SeedCheckResult is the result of a dry-run seed validation.
// Its totals match what Seed would store for the same file and RPC state:
// Found equals what Seeded would be, NotFound and Archived match the seed's
// corresponding counters.
type SeedCheckResult struct {
	// File statistics
	Rows       int      `json:"rows"`        // unique contract IDs parsed from the file
	Duplicates int      `json:"duplicates"`  // duplicate IDs skipped
	Invalid    []string `json:"invalid"`     // invalid contract IDs or malformed rows

	// Network statistics
	Found        int    `json:"found"`         // instances found on RPC, not archived
	NotFound     int    `json:"not_found"`     // instances absent from RPC
	Archived     int    `json:"archived"`      // instances whose TTL has passed
	HintMismatch int    `json:"hint_mismatch"` // file's wasm_hash differed from the instance
	LatestLedger uint32 `json:"latest_ledger"` // RPC latest ledger used for the check

	// Kind breakdown of found (non-archived) instances
	Wasm    int `json:"wasm"`    // wasm kind
	WasmRef int `json:"wasm_ref"` // wasm_ref kind (CAP-85 executable reference)
	SAC     int `json:"sac"`     // Stellar Asset Contract
}

// seedRawRow is one row before ID validation.
type seedRawRow struct {
	line     int
	id       string
	wasmHash string
}

// parseSeedCSV reads the seed CSV and returns all rows (including duplicates
// and invalid IDs), so the caller can count duplicates and collect invalids.
func parseSeedCSV(r io.Reader) (valid []SeedRow, duplicates int, invalid []string, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	first, e := cr.Read()
	if errors.Is(e, io.EOF) {
		return nil, 0, nil, errors.New("seed file is empty")
	}
	if e != nil {
		return nil, 0, nil, fmt.Errorf("seed file line 1: %w", e)
	}
	idCol, hashCol := 0, 1
	var raws []seedRawRow
	line := 2
	if isContractID(strings.TrimSpace(first[0])) {
		raws = append(raws, seedRawRow{line: 1, id: strings.TrimSpace(first[0])})
		if len(first) > 1 {
			raws[0].wasmHash = strings.ToLower(strings.TrimSpace(first[1]))
		}
	} else {
		idCol, hashCol = -1, -1
		for i, h := range first {
			switch strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf")) {
			case "contract_id":
				idCol = i
			case "wasm_hash":
				hashCol = i
			}
		}
		if idCol < 0 {
			return nil, 0, nil, fmt.Errorf("seed file header %v has no contract_id column", first)
		}
	}
	for {
		rec, e := cr.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, 0, nil, fmt.Errorf("seed file: %w", e)
		}
		raw := seedRawRow{line: line}
		line++
		if idCol >= len(rec) {
			invalid = append(invalid, fmt.Sprintf("row %d: no contract_id field", raw.line))
			continue
		}
		raw.id = strings.TrimSpace(rec[idCol])
		if hashCol >= 0 && hashCol < len(rec) {
			raw.wasmHash = strings.ToLower(strings.TrimSpace(rec[hashCol]))
		}
		raws = append(raws, raw)
	}

	seen := map[string]bool{}
	for _, raw := range raws {
		if !isContractID(raw.id) {
			invalid = append(invalid, fmt.Sprintf("row %d: %q is not a contract ID", raw.line, raw.id))
			continue
		}
		if seen[raw.id] {
			duplicates++
			continue
		}
		seen[raw.id] = true
		valid = append(valid, SeedRow{ContractID: raw.id, WasmHash: raw.wasmHash})
	}
	sort.Slice(invalid, func(i, j int) bool { return invalid[i] < invalid[j] })
	return valid, duplicates, invalid, nil
}

// CheckSeed validates a seed file against the network without writing to any
// database. It reads instance entries in the same batches that Seed uses, so
// Found + NotFound + Archived equals Rows and the totals match what Seed
// would store.
func (ix *Indexer) CheckSeed(ctx context.Context, r io.Reader) (SeedCheckResult, error) {
	rows, dups, inv, err := parseSeedCSV(r)
	if err != nil {
		return SeedCheckResult{}, err
	}
	res := SeedCheckResult{
		Rows:       len(rows),
		Duplicates: dups,
		Invalid:    inv,
	}
	if len(inv) > 0 {
		return res, nil // don't hit the network with a broken file
	}

	for start := 0; start < len(rows); start += rpc.MaxLedgerEntryKeys {
		batch := rows[start:min(start+rpc.MaxLedgerEntryKeys, len(rows))]
		if err := ix.checkSeedBatch(ctx, batch, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (ix *Indexer) checkSeedBatch(ctx context.Context, batch []SeedRow, res *SeedCheckResult) error {
	keys := make([]string, 0, len(batch))
	keyToRow := map[string]SeedRow{}
	for _, r := range batch {
		k, err := InstanceKey(r.ContractID)
		if err != nil {
			return err
		}
		keys = append(keys, k)
		keyToRow[k] = r
	}
	got, err := ix.RPC.GetLedgerEntries(ctx, keys)
	if err != nil {
		return fmt.Errorf("checkSeedBatch: %w", err)
	}
	if res.LatestLedger == 0 || got.LatestLedger < res.LatestLedger {
		res.LatestLedger = got.LatestLedger
	}

	found := map[string]bool{}
	for _, e := range got.Entries {
		row, ok := keyToRow[e.Key]
		if !ok {
			continue
		}
		found[e.Key] = true
		archived := e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < got.LatestLedger
		if archived {
			res.Archived++
			continue
		}
		data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{MaxBase64Len: 16 << 20})
		if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
			res.Found++
			res.Wasm++ // count as wasm if undecodable, same as seed
			continue
		}
		inst, err := DecodeInstance(*data.ContractData)
		if err != nil {
			res.Found++
			res.Wasm++
			continue
		}
		if row.WasmHash != "" && inst.Kind == KindWasm && row.WasmHash != inst.WasmHash {
			res.HintMismatch++
			continue
		}
		res.Found++
		switch inst.Kind {
		case KindWasm:
			res.Wasm++
		case KindWasmRef:
			res.WasmRef++
		case KindSAC:
			res.SAC++
		}
	}
	res.NotFound += len(batch) - len(found)
	return nil
}
