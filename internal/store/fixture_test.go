package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// catalogFixture describes a deterministic synthetic index. The same builder
// produces the small database the query-plan tests use and the 10M-claim
// database the benchmark uses, so both exercise the same data shape and
// differ only in size. Nothing is random: two builds of the same size are
// identical row for row.
//
// Shape, for Hashes = H and Contracts = C:
//   - H declaring Wasm hashes, each declaring ten SEPs: hash i declares
//     (i%100)*10+1 .. (i%100)*10+10, so SEP-41 is on 1% of hashes (those
//     with i%100 == 4). That is 10*H wasm_claims rows. One claim in 97
//     carries a leading_zero anomaly; one hash in 50 has a parse anomaly.
//   - H/100 more hashes that declare nothing and match SEP-41: the
//     undeclared gap.
//   - One SEP-41 interface_matches row per hash under the current ruleset
//     (match for i%100 == 4 and for gap hashes, then partial, mismatch and
//     no_spec bands), plus a stale-ruleset row for one hash in ten.
//   - C contracts; contract i runs hash i%(H+H/100). One in 50 is a SAC,
//     one in 100 a wasm_ref, the rest wasm; one in 100 is archived.
//   - One current contract_versions row per contract and a closed earlier
//     version for one contract in ten.
//   - A passing verification for one SEP-41 contract in ten, and a failing
//     one for another.
type catalogFixture struct {
	Hashes    int
	Contracts int
}

// fixtureRuleset is the current SEP-41 ruleset the fixture's matches use.
const fixtureRuleset = "sep41-v0.5.2"

var fixtureRulesets = map[int]string{41: fixtureRuleset}

// fixtureInfo reports what was built, read back from the database.
type fixtureInfo struct {
	Claims, Contracts, Hashes, Versions, Matches, SEP41Hashes int
	IDs                                                       []string // sorted contract IDs
	SEP41Contract, SEP41Hash, UpgradedContract                string
	SizeBytes                                                 int64
}

func (f catalogFixture) gapHashes() int { return f.Hashes / 100 }

func fixtureContractID(i int) (string, error) {
	var raw [32]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(i)) // #nosec G115 -- i is a non-negative loop index
	return strkey.Encode(strkey.VersionByteContract, raw[:])
}

// build creates the database at path with the production migrations and
// indexes, loads the rows, runs ANALYZE, and leaves it in WAL mode exactly
// like a database written by sync.
func (f catalogFixture) build(ctx context.Context, path string) error {
	if f.Hashes < 100 || f.Contracts < 100 {
		return fmt.Errorf("fixture needs at least 100 hashes and contracts, got %d and %d", f.Hashes, f.Contracts)
	}
	s, err := Open(ctx, path, Options{Passphrase: testnet})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	db := s.DB
	// Bulk-load settings only; the file is switched back to WAL afterwards.
	for _, p := range []string{`PRAGMA journal_mode=OFF`, `PRAGMA synchronous=OFF`, `PRAGMA cache_size=-1048576`, `PRAGMA temp_store=MEMORY`} {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	h, g, c := f.Hashes, f.gapHashes(), f.Contracts
	all := h + g
	bulk := []struct {
		what string
		sql  string
		args []any
	}{
		{"wasm", `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?-1)
			INSERT INTO wasm (hash,size_bytes,first_seen_ledger,has_meta,has_spec,sep_entry_count,meta_json,parse_status,parser_version)
			SELECT printf('%064x',i),4096,10,i<?,1,CASE WHEN i<? THEN 10 ELSE 0 END,CASE WHEN i<? THEN '[{"key":"sep","value":"..."}]' ELSE '[]' END,'ok','1' FROM n`,
			[]any{all, h, h, h}},
		{"wasm_claims", `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?-1), tokens(j) AS (VALUES(1) UNION ALL SELECT j+1 FROM tokens WHERE j<10)
			INSERT INTO wasm_claims (wasm_hash,sep,source,raw_token,anomaly)
			SELECT printf('%064x',i),(i%100)*10+j,'sep47-meta',CAST((i%100)*10+j AS TEXT),CASE WHEN (i*10+j)%97=0 THEN 'leading_zero' END FROM n CROSS JOIN tokens`,
			[]any{h}},
		{"parse_anomalies", `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+50 FROM n WHERE i+50<?)
			INSERT INTO parse_anomalies (wasm_hash,raw_token,anomaly) SELECT printf('%064x',i),'abc','non_numeric' FROM n`,
			[]any{h}},
		{"interface_matches", `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?-1)
			INSERT INTO interface_matches (wasm_hash,sep,ruleset_version,status,ok_count,missing_json,mismatched_json,matched_variants_json,computed_at)
			SELECT printf('%064x',i),41,?,
			  CASE WHEN i>=? OR i%100=4 THEN 'match' WHEN i%100<30 THEN 'partial' WHEN i%100<60 THEN 'mismatch' ELSE 'no_spec' END,
			  CASE WHEN i>=? OR i%100=4 THEN 10 WHEN i%100<30 THEN 7 WHEN i%100<60 THEN 2 ELSE 0 END,
			  '[]','[]','{}','2026-11-20T00:00:00Z' FROM n`,
			[]any{all, fixtureRuleset, h, h}},
		{"stale interface_matches", `WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+10 FROM n WHERE i+10<?)
			INSERT INTO interface_matches (wasm_hash,sep,ruleset_version,status,ok_count,missing_json,mismatched_json,matched_variants_json,computed_at)
			SELECT printf('%064x',i),41,'sep41-v0.5.1','match',10,'[]','[]','{}','2026-09-01T00:00:00Z' FROM n`,
			[]any{all}},
	}
	for _, q := range bulk {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("load %s: %w", q.what, err)
		}
	}
	insContract, err := tx.PrepareContext(ctx, `INSERT INTO contracts (contract_id,kind,current_wasm_hash,exec_ref_owner,exec_ref_tag,sac_asset,created_ledger,updated_ledger,archived) VALUES (?,?,?,?,?,?,50,100,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insContract.Close() }()
	insVersion, err := tx.PrepareContext(ctx, `INSERT INTO contract_versions (contract_id,wasm_hash,exec_ref_owner,exec_ref_tag,from_ledger,to_ledger) VALUES (?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insVersion.Close() }()
	insVerification, err := tx.PrepareContext(ctx, `INSERT INTO verifications (contract_id,wasm_hash,sep,tool,tool_version,verdict,passed,clauses_json,run_at) VALUES (?,?,41,'soroban-guard','0.4.2',?,?,'[]','2026-11-01T00:00:00Z')`)
	if err != nil {
		return err
	}
	defer func() { _ = insVerification.Close() }()
	for i := 0; i < c; i++ {
		id, err := fixtureContractID(i)
		if err != nil {
			return err
		}
		hi := i % all
		hash := fixtureHash(hi)
		kind, cur, owner, tag, asset := "wasm", any(hash), any(nil), any(nil), any(nil)
		switch {
		case i%50 == 0:
			kind, cur, asset = "sac", nil, "native"
		case i%100 == 1:
			kind, owner, tag = "wasm_ref", "COWNER", fmt.Sprintf("t%d", i%7)
		}
		if _, err := insContract.ExecContext(ctx, id, kind, cur, owner, tag, asset, i%100 == 2); err != nil {
			return fmt.Errorf("contract %d: %w", i, err)
		}
		from := 50
		if i%10 == 3 {
			old := fixtureHash((hi + 1) % all)
			if kind == "sac" {
				old = ""
			}
			if _, err := insVersion.ExecContext(ctx, id, nullStr(old), owner, tag, 50, 100); err != nil {
				return fmt.Errorf("closed version %d: %w", i, err)
			}
			from = 100
		}
		if _, err := insVersion.ExecContext(ctx, id, cur, owner, tag, from, nil); err != nil {
			return fmt.Errorf("current version %d: %w", i, err)
		}
		if kind != "sac" && hi < h && hi%100 == 4 {
			switch i % 10 {
			case 4:
				_, err = insVerification.ExecContext(ctx, id, hash, "pass", 1)
			case 5:
				_, err = insVerification.ExecContext(ctx, id, hash, "fail", 0)
			}
			if err != nil {
				return fmt.Errorf("verification %d: %w", i, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_state (key,value) VALUES (?, '200') ON CONFLICT (key) DO UPDATE SET value = excluded.value`, KeyLastLedger); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, p := range []string{`ANALYZE`, `PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`} {
		if _, err := db.ExecContext(ctx, p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// inspect reads the fixture's actual counts and representative IDs back,
// and fails if they differ from what the shape promises.
func (f catalogFixture) inspect(ctx context.Context, path string, db *sql.DB) (fixtureInfo, error) {
	var in fixtureInfo
	counts := []struct {
		dst  *int
		q    string
		want int
	}{
		{&in.Claims, `SELECT count(*) FROM wasm_claims`, f.Hashes * 10},
		{&in.Contracts, `SELECT count(*) FROM contracts`, f.Contracts},
		{&in.Hashes, `SELECT count(*) FROM wasm`, f.Hashes + f.gapHashes()},
		{&in.SEP41Hashes, `SELECT count(*) FROM wasm_claims WHERE sep = 41`, f.Hashes / 100},
		{&in.Versions, `SELECT count(*) FROM contract_versions`, f.Contracts + (f.Contracts+6)/10},
		{&in.Matches, `SELECT count(*) FROM interface_matches`, -1},
	}
	for _, c := range counts {
		if err := db.QueryRowContext(ctx, c.q).Scan(c.dst); err != nil {
			return in, fmt.Errorf("%s: %w", c.q, err)
		}
		if c.want >= 0 && *c.dst != c.want {
			return in, fmt.Errorf("%s = %d, want %d (fixture is stale or was built with another size)", c.q, *c.dst, c.want)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT contract_id FROM contracts ORDER BY contract_id`)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return in, err
		}
		in.IDs = append(in.IDs, id)
	}
	_ = rows.Close()
	if !sort.StringsAreSorted(in.IDs) {
		return in, fmt.Errorf("contract IDs are not sorted")
	}
	// Contract 4 runs hash 4, which declares SEP-41 and has a verification;
	// contract 3 has a closed earlier version.
	var err4, err3 error
	in.SEP41Contract, err4 = fixtureContractID(4)
	in.UpgradedContract, err3 = fixtureContractID(3)
	if err4 != nil || err3 != nil {
		return in, fmt.Errorf("fixture IDs: %v %v", err4, err3)
	}
	in.SEP41Hash = fixtureHash(4)
	st, err := os.Stat(path)
	if err != nil {
		return in, err
	}
	in.SizeBytes = st.Size()
	return in, nil
}

// openFixture builds (or, when keep is a path that already holds a fixture of
// this size, reuses) a fixture and returns a read-only store over it, the way
// the API opens a database.
func openFixture(tb testing.TB, f catalogFixture, keep string) (*Store, fixtureInfo, string) {
	tb.Helper()
	ctx := context.Background()
	path := keep
	if path == "" {
		path = tb.TempDir() + "/fixture.db"
	}
	if _, err := os.Stat(path); err != nil {
		if err := f.build(ctx, path); err != nil {
			tb.Fatal(err)
		}
	}
	ro, err := Open(ctx, path, Options{Passphrase: testnet, ReadOnly: true})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = ro.Close() })
	in, err := f.inspect(ctx, path, ro.DB)
	if err != nil {
		tb.Fatal(err)
	}
	return ro, in, path
}

func fixtureHash(i int) string { return fmt.Sprintf("%064x", i) }
