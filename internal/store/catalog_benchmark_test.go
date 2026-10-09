package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// BenchmarkContractsMillionClaims measures the public query, including row
// decoding and cursor encoding, against 1M declarations and 100K live contracts.
// SEP-41 occurs on 1% of hashes; pages therefore scan nonmatching contracts too.
func BenchmarkContractsMillionClaims(b *testing.B) {
	ctx := context.Background()
	db := filepath.Join(b.TempDir(), "million.db")
	s, err := Open(ctx, db, Options{Passphrase: "benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Bulk creation preserves the production schema and all existing indexes.
	for _, q := range []string{
		`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<99999) INSERT INTO wasm (hash,has_meta,has_spec,sep_entry_count,meta_json,parse_status,parser_version) SELECT printf('%064x',i),1,0,10,'[]','ok','1' FROM n`,
		`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<99999), tokens(j) AS (VALUES(1) UNION ALL SELECT j+1 FROM tokens WHERE j<10) INSERT INTO wasm_claims (wasm_hash,sep,source,raw_token) SELECT printf('%064x',i),(i%100)*10+j,'sep47-meta',CAST((i%100)*10+j AS TEXT) FROM n CROSS JOIN tokens`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO contracts (contract_id,kind,current_wasm_hash,updated_ledger,archived) VALUES (?,'wasm',?,100,0)`)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 100000)
	for i := range ids {
		var raw [32]byte
		binary.BigEndian.PutUint64(raw[:8], uint64(i))
		id, err := strkey.Encode(strkey.VersionByteContract, raw[:])
		if err != nil {
			b.Fatal(err)
		}
		ids[i] = id
		if _, err = stmt.ExecContext(ctx, id, fmt.Sprintf("%064x", i)); err != nil {
			b.Fatal(err)
		}
	}
	if err = stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err = s.Close(); err != nil {
		b.Fatal(err)
	}
	ro, err := Open(ctx, db, Options{Passphrase: "benchmark", ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	var claims, contracts, sep41 int
	if err = ro.DB.QueryRowContext(ctx, `SELECT count(*) FROM wasm_claims`).Scan(&claims); err != nil {
		b.Fatal(err)
	}
	if err = ro.DB.QueryRowContext(ctx, `SELECT count(*) FROM contracts`).Scan(&contracts); err != nil {
		b.Fatal(err)
	}
	if err = ro.DB.QueryRowContext(ctx, `SELECT count(*) FROM wasm_claims WHERE sep=41`).Scan(&sep41); err != nil {
		b.Fatal(err)
	}
	if claims != 1000000 || contracts != 100000 || sep41 != 1000 {
		b.Fatal(claims, contracts, sep41)
	}
	b.Logf("fixture: %d claims, %d live contracts, %d SEP-41 hashes; production indexes, read-only query, limit 50", claims, contracts, sep41)
	sort.Strings(ids)
	for i := range 5 {
		if _, err = ro.Contracts(ctx, Filter{Implements: []int{41}, Limit: 50, Cursor: encodeContractCursor(ids[i*15000])}); err != nil {
			b.Fatal(err)
		}
	}
	durations := []time.Duration{}
	iteration := 0
	for b.Loop() {
		// Traverse deterministic cursor positions in the first 80% of the ID
		// space, keeping enough results for a full page at every position.
		cursor := encodeContractCursor(ids[(iteration*7919)%80000])
		start := time.Now()
		page, err := ro.Contracts(ctx, Filter{Implements: []int{41}, Limit: 50, Cursor: cursor})
		durations = append(durations, time.Since(start))
		if err != nil || len(page.Contracts) != 50 {
			b.Fatal(err, len(page.Contracts))
		}
		iteration++
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	b.ReportMetric(float64(durations[(len(durations)-1)*50/100])/float64(time.Millisecond), "p50-ms")
	b.ReportMetric(float64(durations[(len(durations)-1)*95/100])/float64(time.Millisecond), "p95-ms")
}
