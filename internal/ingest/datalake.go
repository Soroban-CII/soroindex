package ingest

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DataLakeSource reads a Hubble-format contract export and implements
// BackfillSource. It is used to seed a new index from a data-lake snapshot
// without replaying the full ledger history.
//
// # Source schema
//
// The export is produced by scripts/hubble-export.sql Q2 from Stellar Hubble
// (project crypto-stellar, tables contract_data_snapshot and contract_code).
// Each row is a non-deleted contract instance. Required columns:
//
//	contract_id  — C... strkey of the deployed contract
//	wasm_hash    — optional lowercase hex SHA-256 of the contract's Wasm code;
//	               omit or leave empty when the value is unavailable
//
// Additional columns are silently ignored, so the full Hubble instance export
// can be passed without filtering. A BOM prefix on the first header field is
// accepted.
//
// # Network provenance
//
// The caller must supply the expected network passphrase. The source records it
// in its provenance metadata but does not verify it against a live RPC node;
// the Indexer.Seed caller is responsible for that check. If Passphrase is empty
// the source rejects all calls, because a seed without a network identity can
// silently mix mainnet and testnet data.
//
// # Date bounds and cost
//
// Hubble charges per byte scanned (BigQuery pricing, ~$5/TB as of 2026-10).
// scripts/hubble-export.sql uses partition pruning and column projection to
// limit cost. For a full mainnet snapshot the query typically scans a few GiB.
//
// # Nonconsecutive replay
//
// The data-lake source does not support incremental updates. It is a one-time
// seed only. If the index has already advanced past AsOfLedger the caller
// should not re-seed; the Indexer.Seed function treats seeding as idempotent
// but re-seeding older data can lower last_ledger, forcing a re-sync.
type DataLakeSource struct {
	// R is the export CSV. The caller is responsible for closing it.
	R io.Reader
	// Passphrase is the network this export belongs to, e.g.
	// config.Mainnet.Passphrase. Must not be empty.
	Passphrase string
	// AsOfLedger is the ledger sequence at which the export was taken.
	// When non-zero the indexer uses it as the starting point for incremental
	// sync, so ledgers after the snapshot are not skipped.
	AsOfLedger uint32
}

var (
	// ErrMissingPassphrase means DataLakeSource.Passphrase was not set.
	ErrMissingPassphrase = errors.New("datalake: Passphrase is required to prevent cross-network contamination")
)

// Name implements BackfillSource.
func (s *DataLakeSource) Name() string {
	return fmt.Sprintf("hubble-export(passphrase=%q,as_of_ledger=%d)", s.Passphrase, s.AsOfLedger)
}

// Contracts implements BackfillSource. It reads the CSV export from s.R and
// returns the unique set of contract rows.
//
// Errors:
//   - ErrMissingPassphrase if Passphrase is empty.
//   - A parse error if the CSV is malformed or missing the contract_id column.
//   - A validation error if any contract_id is not a valid C... strkey.
func (s *DataLakeSource) Contracts(_ context.Context) ([]SeedRow, error) {
	if s.Passphrase == "" {
		return nil, ErrMissingPassphrase
	}

	cr := csv.NewReader(s.R)
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("datalake: export file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("datalake: read header: %w", err)
	}

	idCol, hashCol := -1, -1
	for i, h := range header {
		clean := strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf"))
		switch clean {
		case "contract_id":
			idCol = i
		case "wasm_hash":
			hashCol = i
		}
	}
	if idCol < 0 {
		return nil, fmt.Errorf("datalake: header %v has no contract_id column", header)
	}

	seen := map[string]bool{}
	var out []SeedRow
	lineNum := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("datalake: line %d: %w", lineNum, err)
		}
		lineNum++
		if idCol >= len(rec) {
			return nil, fmt.Errorf("datalake: line %d: no contract_id field", lineNum)
		}
		id := strings.TrimSpace(rec[idCol])
		if !isContractID(id) {
			return nil, fmt.Errorf("datalake: line %d: %q is not a valid contract ID", lineNum, id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r := SeedRow{ContractID: id}
		if hashCol >= 0 && hashCol < len(rec) {
			r.WasmHash = strings.ToLower(strings.TrimSpace(rec[hashCol]))
		}
		out = append(out, r)
	}
	return out, nil
}
