package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// The materialized design measured by BenchmarkMaterializedDesign. It is an
// experiment for the decision record in docs/query-benchmark.md, not a
// migration: the tables live in a separate scratch file attached to the
// fixture, and nothing here changes the production schema.
//
//   - current_claims is the claims view materialized for the declared tier:
//     one row per (SEP, live non-SAC contract) for the contract's current
//     Wasm, keyed so a catalog page is a range read.
//   - sep_counts holds /v1/seps' four tier counts per SEP.
//   - stat_values holds the /v1/stats numbers as key/value rows.
var materializedSchema = []string{
	`CREATE TABLE mat.current_claims (sep INTEGER NOT NULL, contract_id TEXT NOT NULL, PRIMARY KEY (sep, contract_id)) WITHOUT ROWID`,
	`CREATE TABLE mat.sep_counts (sep INTEGER PRIMARY KEY, declared INTEGER NOT NULL, inferred INTEGER NOT NULL, verified INTEGER NOT NULL, protocol INTEGER NOT NULL)`,
	`CREATE TABLE mat.stat_values (key TEXT PRIMARY KEY, value INTEGER NOT NULL) WITHOUT ROWID`,
}

// materializedRefresh rebuilds every table from the source rows, as a sync
// would have to after each batch without incremental maintenance. The SEP
// counts reuse the production /v1/seps query text so both designs count the
// same thing.
func materializedRefresh(ctx context.Context, db *sql.DB, s *Store) (map[string]time.Duration, error) {
	took := map[string]time.Duration{}
	versions, err := json.Marshal(fixtureRulesets)
	if err != nil {
		return nil, err
	}
	steps := []struct {
		name string
		sql  string
		args []any
	}{
		{"current_claims", `INSERT INTO mat.current_claims (sep, contract_id)
			SELECT DISTINCT wc.sep, c.contract_id FROM contracts c JOIN wasm_claims wc ON wc.wasm_hash = c.current_wasm_hash
			WHERE c.archived = 0 AND c.kind <> 'sac'`, nil},
		{"sep_counts", `INSERT INTO mat.sep_counts (sep, declared, inferred, verified, protocol)
			WITH evidence AS (
			 SELECT sep, contract_id, 'declared' AS tier FROM mat.current_claims
			 UNION ALL
			 SELECT DISTINCT im.sep,c.contract_id,'inferred' FROM contracts c JOIN interface_matches im ON im.wasm_hash=c.current_wasm_hash WHERE c.archived=0 AND c.kind<>'sac' AND im.status='match' AND im.ruleset_version=json_extract(?, '$.' || im.sep)
			 UNION ALL
			 SELECT DISTINCT v.sep,c.contract_id,'verified' FROM contracts c JOIN verifications v ON v.contract_id=c.contract_id AND v.wasm_hash=c.current_wasm_hash WHERE c.archived=0 AND c.kind<>'sac' AND v.passed=1 AND v.verdict='pass' AND EXISTS (SELECT 1 FROM wasm_claims wc WHERE wc.wasm_hash=v.wasm_hash AND wc.sep=v.sep) AND NOT EXISTS (SELECT 1 FROM verifications newer WHERE newer.contract_id=v.contract_id AND newer.wasm_hash=v.wasm_hash AND newer.sep=v.sep AND newer.run_at>v.run_at)
			 UNION ALL
			 SELECT 41,c.contract_id,'protocol' FROM contracts c WHERE c.archived=0 AND c.kind='sac'
			) SELECT sep,sum(tier='declared'),sum(tier='inferred'),sum(tier='verified'),sum(tier='protocol') FROM evidence GROUP BY sep`, []any{string(versions)}},
	}
	for _, st := range steps {
		t0 := time.Now()
		if _, err := db.ExecContext(ctx, st.sql, st.args...); err != nil {
			return nil, fmt.Errorf("refresh %s: %w", st.name, err)
		}
		took[st.name] = time.Since(t0)
	}
	// stat_values: the production Stats call computes the numbers once.
	t0 := time.Now()
	d, err := s.Stats(ctx, "testnet", fixtureRulesets)
	if err != nil {
		return nil, err
	}
	values := map[string]int{
		"live_sac": d.Adoption.LiveSAC, "live_wasm": d.Adoption.LiveWasm, "live_wasm_ref": d.Adoption.LiveWasmRef,
		"measured_contracts": d.Adoption.MeasuredContracts, "used_hashes": d.Adoption.UsedHashes,
		"declaring_contracts": d.Adoption.DeclaringContracts, "declaring_used_hashes": d.Adoption.DeclaringUsedHashes,
		"gap_contracts": d.Adoption.GapContracts, "gap_used_hashes": d.Adoption.GapUsedHashes,
		"archived_contracts": d.ArchivedContracts, "total_contracts": d.TotalContracts,
	}
	for k, v := range d.Anomalies {
		values["anomaly:"+k] = v
	}
	for k, v := range d.Adoption.SEP41ByContract {
		values["sep41_contracts:"+k] = v
	}
	for k, v := range values {
		if _, err := db.ExecContext(ctx, `INSERT INTO mat.stat_values (key, value) VALUES (?, ?)`, k, v); err != nil {
			return nil, err
		}
	}
	took["stat_values"] = time.Since(t0)
	return took, nil
}

// BenchmarkMaterializedDesign measures the materialized alternative on the
// same fixture as BenchmarkAPIQueries: the full refresh cost, the read cost
// of the queries it replaces, and the per-upgrade cost of maintaining the
// declared tier incrementally inside a sync transaction.
func BenchmarkMaterializedDesign(b *testing.B) {
	ctx := context.Background()
	f, keep := benchFixture(b)
	ro, in, path := openFixture(b, f, keep)

	// main is an empty in-memory database, so unqualified names (contracts,
	// wasm_claims, ...) resolve to the attached fixture, which is only read.
	// The materialized tables go in their own scratch file, "mat".
	matPath := filepath.Join(b.TempDir(), "materialized.db")
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	for _, q := range []struct {
		sql string
		arg string
	}{{`ATTACH DATABASE ? AS mat`, matPath}, {`ATTACH DATABASE ? AS src`, path}} {
		if _, err := db.ExecContext(ctx, q.sql, q.arg); err != nil {
			b.Fatal(err)
		}
	}
	for _, q := range append([]string{`PRAGMA mat.journal_mode=WAL`, `PRAGMA mat.synchronous=NORMAL`}, materializedSchema...) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			b.Fatal(err)
		}
	}

	t0 := time.Now()
	took, err := materializedRefresh(ctx, db, ro)
	if err != nil {
		b.Fatal(err)
	}
	refresh := time.Since(t0)
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mat.current_claims`).Scan(&rows); err != nil {
		b.Fatal(err)
	}
	st, err := os.Stat(matPath)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("full refresh %s (current_claims %s, sep_counts %s, stat_values %s); current_claims %d rows; materialized file %d bytes",
		refresh.Round(time.Millisecond), took["current_claims"].Round(time.Millisecond), took["sep_counts"].Round(time.Millisecond),
		took["stat_values"].Round(time.Millisecond), rows, st.Size())
	if _, err := db.ExecContext(ctx, `ANALYZE mat`); err != nil {
		b.Fatal(err)
	}

	// Each read returns what the production query returns for the same
	// input, decoded the same way.
	page := func(q string, args ...any) (int, error) {
		r, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		defer func() { _ = r.Close() }()
		n := 0
		for r.Next() {
			var id, kind string
			var hash, owner, tag sql.NullString
			var archived bool
			if err := r.Scan(&id, &kind, &hash, &owner, &tag, &archived); err != nil {
				return n, err
			}
			n++
		}
		return n, r.Err()
	}
	const catalog = `SELECT c.contract_id,c.kind,c.current_wasm_hash,c.exec_ref_owner,c.exec_ref_tag,c.archived
		FROM mat.current_claims m JOIN contracts c ON c.contract_id = m.contract_id
		WHERE m.sep = ? AND m.contract_id > ? ORDER BY m.contract_id LIMIT 51`
	reads := []apiQuery{
		{name: "read/contracts/declared/implements=41", fullPage: true, run: func(_ context.Context, _ *Store, in fixtureInfo, i int) (int, error) {
			n, err := page(catalog, 41, in.IDs[(i*7919)%(len(in.IDs)*8/10)])
			return min(n, 50), err
		}},
		{name: "read/contracts/declared/implements=1001-none", run: func(_ context.Context, _ *Store, _ fixtureInfo, _ int) (int, error) {
			return page(catalog, 1001, "")
		}},
		{name: "read/seps", run: func(context.Context, *Store, fixtureInfo, int) (int, error) {
			r, err := db.QueryContext(ctx, `SELECT sep,declared,inferred,verified,protocol FROM mat.sep_counts WHERE sep > ? ORDER BY sep LIMIT 51`, 0)
			if err != nil {
				return 0, err
			}
			defer func() { _ = r.Close() }()
			n := 0
			for r.Next() {
				var c SEPCounts
				if err := r.Scan(&c.SEP, &c.Declared, &c.Inferred, &c.Verified, &c.Protocol); err != nil {
					return n, err
				}
				n++
			}
			return n, r.Err()
		}},
		{name: "read/stats", run: func(context.Context, *Store, fixtureInfo, int) (int, error) {
			r, err := db.QueryContext(ctx, `SELECT key, value FROM mat.stat_values`)
			if err != nil {
				return 0, err
			}
			defer func() { _ = r.Close() }()
			n := 0
			for r.Next() {
				var k string
				var v int
				if err := r.Scan(&k, &v); err != nil {
					return n, err
				}
				n++
			}
			return n, r.Err()
		}},
	}
	for _, q := range reads {
		b.Run(q.name, func(b *testing.B) {
			for i := range 3 {
				if _, err := q.run(ctx, nil, in, i); err != nil {
					b.Fatal(err)
				}
			}
			var d []time.Duration
			i := 0
			for b.Loop() {
				t := time.Now()
				n, err := q.run(ctx, nil, in, i)
				d = append(d, time.Since(t))
				if err != nil || (q.fullPage && n != 50) {
					b.Fatal(err, n)
				}
				i++
			}
			sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
			b.ReportMetric(percentile(d, 50), "p50-ms")
			b.ReportMetric(percentile(d, 95), "p95-ms")
		})
	}

	// Write side: what each contract upgrade would add to a sync batch to
	// keep the declared tier current. Each iteration moves a different
	// Wasm contract to the next hash in its own transaction; SACs, which
	// have no declared claims to move, are passed over and not timed.
	b.Run("write/upgrade-one-contract", func(b *testing.B) {
		var d []time.Duration
		i := 0
		for b.Loop() {
			for {
				id := in.IDs[(i*7919)%len(in.IDs)]
				i++
				t := time.Now()
				moved, err := materializedUpgrade(ctx, db, id)
				if err != nil {
					b.Fatal(err)
				}
				if moved {
					d = append(d, time.Since(t))
					break
				}
			}
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		b.ReportMetric(percentile(d, 50), "p50-ms")
		b.ReportMetric(percentile(d, 95), "p95-ms")
	})
}

// materializedUpgrade applies one upgrade of contract id to the declared
// tier's materialized rows: drop the old hash's claims, add the new hash's,
// and move the per-SEP counts. The source tables are not written; the new
// hash is the next fixture hash, read from wasm_claims like sync would.
func materializedUpgrade(ctx context.Context, db *sql.DB, id string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() // after Commit this is a no-op
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_wasm_hash FROM contracts WHERE contract_id = ?`, id).Scan(&old); err != nil {
		return false, err
	}
	if !old.Valid {
		return false, nil // a SAC has no declared claims to move
	}
	var next string
	if err := tx.QueryRowContext(ctx, `SELECT hash FROM wasm WHERE hash > ? ORDER BY hash LIMIT 1`, old.String).Scan(&next); err != nil {
		return false, err
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		// Every statement is driven by the old or new hash's SEP list, so
		// each is a primary-key lookup on (sep, contract_id) or sep.
		{`UPDATE mat.sep_counts SET declared = declared - 1 WHERE sep IN (SELECT DISTINCT sep FROM wasm_claims WHERE wasm_hash = ?)`, []any{old.String}},
		{`DELETE FROM mat.current_claims WHERE sep IN (SELECT DISTINCT sep FROM wasm_claims WHERE wasm_hash = ?) AND contract_id = ?`, []any{old.String, id}},
		{`INSERT OR IGNORE INTO mat.current_claims (sep, contract_id) SELECT DISTINCT sep, ? FROM wasm_claims WHERE wasm_hash = ?`, []any{id, next}},
		{`INSERT INTO mat.sep_counts (sep, declared, inferred, verified, protocol) SELECT DISTINCT sep, 1, 0, 0, 0 FROM wasm_claims WHERE wasm_hash = ? AND true
			ON CONFLICT (sep) DO UPDATE SET declared = declared + 1`, []any{next}},
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s.sql, s.args...); err != nil {
			return false, fmt.Errorf("upgrade %s: %w", id, err)
		}
	}
	return true, tx.Commit()
}
