package store

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"hash/fnv"

	sqlite "modernc.org/sqlite"
)

// sampleRank is a deterministic pseudo-random ordering over contract IDs.
// It lets the data-quality checker draw a reproducible sample with one
// ordered query instead of scanning offsets or loading every ID.
func sampleRank(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 2 {
		return nil, errors.New("soroindex_sample_rank: want 2 arguments")
	}
	seed, ok := args[0].(int64)
	if !ok {
		return nil, errors.New("soroindex_sample_rank: seed must be an integer")
	}
	var id []byte
	switch v := args[1].(type) {
	case string:
		id = []byte(v)
	case []byte:
		id = v
	default:
		return nil, errors.New("soroindex_sample_rank: contract ID must be text")
	}
	h := fnv.New64a()
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(seed)) // #nosec G115 -- a seed is an opaque bit pattern
	_, _ = h.Write(b[:])                           // hash.Write never returns an error
	_, _ = h.Write(id)
	return int64(h.Sum64()), nil // #nosec G115 -- ordering key, not a count
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("soroindex_sample_rank", 2, sampleRank)
}

// ContractState is the stored executable of one contract, as the
// data-quality checker compares it with the network.
type ContractState struct {
	ID            string
	Kind          string
	WasmHash      string
	ExecRefOwner  string
	ExecRefTag    string
	SACAsset      string
	UpdatedLedger uint32
	Archived      bool
}

// SampleContracts returns up to n contracts in a deterministic order that
// depends on seed. The same database and seed always produce the same rows;
// a different seed produces a different order. Rows are read in one query.
func (s *Store) SampleContracts(ctx context.Context, n int, seed int64) ([]ContractState, error) {
	if n < 1 || n > 100000 {
		return nil, ErrBadQuery
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT contract_id, kind, COALESCE(current_wasm_hash, ''), COALESCE(exec_ref_owner, ''),
       COALESCE(exec_ref_tag, ''), COALESCE(sac_asset, ''), COALESCE(updated_ledger, 0), archived
FROM contracts
ORDER BY soroindex_sample_rank(?, contract_id)
LIMIT ?`, seed, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ContractState{}
	for rows.Next() {
		var c ContractState
		var updated int64
		if err := rows.Scan(&c.ID, &c.Kind, &c.WasmHash, &c.ExecRefOwner, &c.ExecRefTag, &c.SACAsset, &updated, &c.Archived); err != nil {
			return nil, err
		}
		if c.UpdatedLedger, err = toLedger(updated); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
