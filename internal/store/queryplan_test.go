package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"flag"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	sqlite "modernc.org/sqlite"
)

// tracedQuery is one statement the store sent to SQLite, with its arguments.
type tracedQuery struct {
	sql  string
	args []any
}

// queryTrace records every statement run through connections opened by the
// "sqlite-trace" driver. Plan tests drive the real Store methods through it,
// so they EXPLAIN exactly the SQL production runs, including any statement
// a later change adds.
type queryTrace struct {
	mu      sync.Mutex
	queries []tracedQuery
}

func (q *queryTrace) record(query string, args []driver.NamedValue) {
	vals := make([]any, len(args))
	for i, a := range args {
		vals[i] = a.Value
	}
	q.mu.Lock()
	q.queries = append(q.queries, tracedQuery{query, vals})
	q.mu.Unlock()
}

func (q *queryTrace) take() []tracedQuery {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.queries
	q.queries = nil
	return out
}

var activeTrace = &queryTrace{}

type traceDriver struct{ inner sqlite.Driver }

func (d *traceDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &traceConn{Conn: c}, nil
}

// traceConn forwards to the modernc connection and records each query.
type traceConn struct{ driver.Conn }

func (c *traceConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	activeTrace.record(query, args)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *traceConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	activeTrace.record(query, args)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *traceConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *traceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *traceConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func init() { sql.Register("sqlite-trace", &traceDriver{}) }

// planLine is one row of EXPLAIN QUERY PLAN.
type planLine struct {
	id, parent int
	detail     string
}

func explain(ctx context.Context, db *sql.DB, q tracedQuery) ([]planLine, error) {
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+q.sql, q.args...) // #nosec G202 -- traced store SQL
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []planLine
	for rows.Next() {
		var l planLine
		var unused int
		if err := rows.Scan(&l.id, &l.parent, &unused, &l.detail); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// renderPlan prints a plan as the indented tree sqlite3's .eqp shows.
func renderPlan(plan []planLine) string {
	depth := map[int]int{0: -1}
	var b strings.Builder
	for _, l := range plan {
		d := depth[l.parent] + 1
		depth[l.id] = d
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", d), l.detail)
	}
	return b.String()
}

// fullScan matches a plan step that reads every row of wasm_claims or
// contract_versions, whether through the table or by walking one of its
// indexes end to end. The store's SQL refers to wasm_claims as wc or by name
// and to contract_versions only by name.
var fullScan = regexp.MustCompile(`^SCAN (wasm_claims|wc|contract_versions)\b`)

// dumpPlans prints every traced statement and its plan:
//
//	go test ./internal/store -run TestQueryPlansUseIntendedIndexes -v -args -dump-plans
var dumpPlans = flag.Bool("dump-plans", false, "print every API query and its EXPLAIN QUERY PLAN")

var spaces = regexp.MustCompile(`\s+`)

// normalizeSQL collapses whitespace so expectations can match one line.
func normalizeSQL(q string) string { return strings.TrimSpace(spaces.ReplaceAllString(q, " ")) }

// planQuery names one statement by a regular expression over its
// whitespace-normalized SQL and lists the indexes its plan must use.
type planQuery struct {
	match   string
	indexes []string
	// fullScanReason, when set, documents an accepted full scan of
	// wasm_claims. The test still asserts the scan, so the exception cannot
	// spread to other queries or change silently.
	fullScanReason string
}

// planCase drives one Store method and lists every statement it runs.
type planCase struct {
	name    string
	run     func(context.Context, *Store, fixtureInfo) error
	queries []planQuery
}

const (
	idxContractsPK = "sqlite_autoindex_contracts_1"
	idxClaimsPK    = "sqlite_autoindex_wasm_claims_1"
	idxMatchesPK   = "sqlite_autoindex_interface_matches_1"
	idxVerifPK     = "sqlite_autoindex_verifications_1"
	idxVersionsPK  = "sqlite_autoindex_contract_versions_1"
	idxWasmPK      = "sqlite_autoindex_wasm_1"
	idxAnomaliesPK = "sqlite_autoindex_parse_anomalies_1"
	idxStatePK     = "sqlite_autoindex_sync_state_1"
)

var lastLedgerQuery = planQuery{match: `^SELECT value FROM sync_state WHERE key = \?$`, indexes: []string{idxStatePK}}

// catalogQuery is the one statement behind GET /v1/contracts.
const catalogQuery = `^SELECT c\.contract_id,c\.kind,.* FROM contracts c WHERE `

// measured is the "live, parsed Wasm contracts" subquery totals() builds.
const measured = `\(SELECT c\.contract_id, c\.current_wasm_hash AS h FROM contracts c JOIN wasm w ON w\.hash = c\.current_wasm_hash WHERE c\.archived = 0 AND c\.kind IN \('wasm','wasm_ref'\) AND w\.parse_status <> 'archived'\)`

// detailQueries are the per-version claim lookups contract detail and
// history share.
var detailQueries = []planQuery{
	{match: `^SELECT wasm_hash,from_ledger,to_ledger,exec_ref_owner,exec_ref_tag,created_tx_hash,created_source_account FROM contract_versions WHERE contract_id = \?`, indexes: []string{idxVersionsPK}},
	{match: `^SELECT sep,source,anomaly FROM wasm_claims WHERE wasm_hash = \?`, indexes: []string{idxClaimsPK}},
	{match: `^SELECT sep,status,ruleset_version,missing_json,mismatched_json FROM interface_matches WHERE wasm_hash = \?`, indexes: []string{idxMatchesPK}},
	{match: `^SELECT sep,verdict,tool,tool_version,run_at FROM verifications WHERE contract_id = \?`, indexes: []string{idxVerifPK}},
}

// planCases covers every query behind the API and CLI: the catalog in every
// tier and filter form, contract detail, history, Wasm detail, SEP counts,
// stats and the gap. The expected index names are the ones each plan uses
// on the fixture after ANALYZE.
var planCases = []planCase{
	{
		name: "catalog declared implements=41",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Implements: []int{41}, Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, "idx_claims_sep"}}},
	},
	{
		name: "catalog declared with no SEP filter",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, idxClaimsPK}}},
	},
	{
		name: "catalog declared implements=41,42 (AND)",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Implements: []int{41, 42}, Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, "idx_claims_sep"}}},
	},
	{
		name: "catalog declared implements_any=41,951 (OR)",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{ImplementsAny: []int{41, 951}, Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, "idx_claims_sep"}}},
	},
	{
		name: "catalog declared kind=wasm_ref with a cursor",
		run: func(ctx context.Context, s *Store, in fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Implements: []int{41}, Kind: "wasm_ref", Limit: 50, Cursor: encodeContractCursor(in.IDs[len(in.IDs)/3])})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, "idx_claims_sep"}}},
	},
	{
		name: "catalog inferred implements=41",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Tier: "inferred", Implements: []int{41}, Limit: 50, Rulesets: fixtureRulesets})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, idxMatchesPK}}},
	},
	{
		name: "catalog verified implements=41",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Tier: "verified", Implements: []int{41}, Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK, idxVerifPK, "idx_claims_sep"}}},
	},
	{
		name: "catalog protocol implements=41",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Contracts(ctx, Filter{Tier: "protocol", Implements: []int{41}, Limit: 50})
			return err
		},
		queries: []planQuery{{match: catalogQuery, indexes: []string{idxContractsPK}}},
	},
	{
		name: "contract detail",
		run: func(ctx context.Context, s *Store, in fixtureInfo) error {
			_, err := s.Contract(ctx, in.SEP41Contract, "testnet", fixtureRulesets)
			return err
		},
		queries: append([]planQuery{
			{match: `^SELECT kind,current_wasm_hash,exec_ref_owner,exec_ref_tag,archived FROM contracts WHERE contract_id = \?$`, indexes: []string{idxContractsPK}},
			lastLedgerQuery,
		}, detailQueries...),
	},
	{
		name: "contract history",
		run: func(ctx context.Context, s *Store, in fixtureInfo) error {
			_, err := s.HistoryPage(ctx, in.UpgradedContract, "testnet", fixtureRulesets, 50, "")
			return err
		},
		queries: append([]planQuery{
			{match: `^SELECT current_wasm_hash FROM contracts WHERE contract_id=\?$`, indexes: []string{idxContractsPK}},
			lastLedgerQuery,
		}, detailQueries...),
	},
	{
		name: "wasm detail",
		run: func(ctx context.Context, s *Store, in fixtureInfo) error {
			_, err := s.Wasm(ctx, in.SEP41Hash, 50, "", fixtureRulesets)
			return err
		},
		queries: []planQuery{
			{match: `^SELECT meta_json,parse_status,parse_error,parser_version FROM wasm WHERE hash = \?$`, indexes: []string{idxWasmPK}},
			lastLedgerQuery,
			{match: `^SELECT sep,source,raw_token,anomaly FROM wasm_claims WHERE wasm_hash = \?`, indexes: []string{idxClaimsPK}},
			{match: `^SELECT raw_token,anomaly FROM parse_anomalies WHERE wasm_hash = \?`, indexes: []string{idxAnomaliesPK}},
			{match: `^SELECT sep,ruleset_version,status,ok_count,missing_json,mismatched_json,matched_variants_json FROM interface_matches WHERE wasm_hash = \?`, indexes: []string{idxMatchesPK}},
			{match: `^SELECT contract_id,kind,current_wasm_hash,exec_ref_owner,exec_ref_tag,archived FROM contracts WHERE current_wasm_hash = \?`, indexes: []string{"idx_contracts_hash"}},
		},
	},
	{
		name: "seps",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.SEPs(ctx, 50, "", fixtureRulesets)
			return err
		},
		queries: []planQuery{
			lastLedgerQuery,
			{match: `^WITH evidence AS`, indexes: []string{idxClaimsPK, "idx_matches_sep", "idx_contracts_hash", idxVerifPK, "idx_claims_sep"}},
		},
	},
	{
		name: "stats",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Stats(ctx, "testnet", fixtureRulesets)
			return err
		},
		queries: []planQuery{
			lastLedgerQuery,
			// The live-contract counts read every contracts row by design.
			{match: `^SELECT count\(\*\) FROM contracts c WHERE c\.archived = 0 AND kind = 'sac'$`},
			{match: `^SELECT count\(\*\) FROM contracts c WHERE c\.archived = 0 AND kind = 'wasm'$`},
			{match: `^SELECT count\(\*\) FROM contracts c WHERE c\.archived = 0 AND kind = 'wasm_ref'$`},
			{match: `^SELECT count\(\*\) FROM contracts c WHERE c\.archived = 0 AND kind = 'wasm_ref' AND current_wasm_hash IS NULL$`},
			{match: `^SELECT count\(\*\) FROM ` + measured + `$`, indexes: []string{idxWasmPK}},
			{match: `^SELECT count\(DISTINCT h\) FROM ` + measured + `$`, indexes: []string{idxWasmPK}},
			{match: `^SELECT count\(\*\) FROM ` + measured + ` m WHERE EXISTS`, indexes: []string{idxWasmPK, idxClaimsPK}},
			{match: `^SELECT count\(DISTINCT h\) FROM ` + measured + ` m WHERE EXISTS`, indexes: []string{idxWasmPK, idxClaimsPK}},
			{match: `^SELECT count\(\*\) FROM ` + measured + ` m JOIN interface_matches`, indexes: []string{"idx_matches_sep", "idx_contracts_hash", idxWasmPK, idxClaimsPK}},
			{match: `^SELECT count\(DISTINCT m\.h\) FROM ` + measured + ` m JOIN interface_matches`, indexes: []string{"idx_matches_sep", "idx_contracts_hash", idxWasmPK, idxClaimsPK}},
			{match: `^SELECT im\.status, count\(\*\), count\(DISTINCT m\.h\) FROM`, indexes: []string{"idx_matches_sep", "idx_contracts_hash", idxWasmPK}},
			{match: `^SELECT COALESCE\(sum\(archived\),0\),count\(\*\) FROM contracts$`},
			{match: `^SELECT count\(\*\) FROM wasm_fetch_queue$`},
			{match: `FROM parse_anomalies UNION ALL SELECT anomaly FROM wasm_claims WHERE anomaly IS NOT NULL`,
				fullScanReason: "anomaly totals count every wasm_claims row with an anomaly; no index covers wasm_claims.anomaly, and adding one is a schema change that needs maintainer approval (see docs/query-benchmark.md)"},
		},
	},
	{
		name: "gap sep=41",
		run: func(ctx context.Context, s *Store, _ fixtureInfo) error {
			_, err := s.Gap(ctx, 41, fixtureRuleset, 50, "")
			return err
		},
		queries: []planQuery{{match: `^SELECT c\.contract_id, c\.current_wasm_hash, im\.ok_count FROM contracts c JOIN interface_matches im`, indexes: []string{"idx_matches_sep", "idx_contracts_hash", idxClaimsPK}}},
	},
}

// tracedStore opens a read-only store over path through the tracing driver,
// with the same DSN options Open uses for the API.
func tracedStore(tb testing.TB, path string) *Store {
	tb.Helper()
	db, err := sql.Open("sqlite-trace", "file:"+path+"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return &Store{DB: db, readOnly: true}
}

// checkPlans runs every plan case against the store at path and returns the
// failures. It is shared by the plan test and the 10M benchmark, so plans are
// asserted on both the small and the large fixture.
func checkPlans(ctx context.Context, tb testing.TB, path string, plain *sql.DB, in fixtureInfo) {
	tb.Helper()
	traced := tracedStore(tb, path)
	for _, pc := range planCases {
		activeTrace.take()
		if err := pc.run(ctx, traced, in); err != nil {
			tb.Errorf("%s: %v", pc.name, err)
			continue
		}
		seen := map[int]bool{}
		for _, q := range activeTrace.take() {
			idx := -1
			for i, want := range pc.queries {
				if regexp.MustCompile(want.match).MatchString(normalizeSQL(q.sql)) {
					if idx >= 0 {
						tb.Errorf("%s: statement matches two expectations (%q and %q); make the fragments unique:\n%s", pc.name, pc.queries[idx].match, want.match, q.sql)
					}
					idx = i
				}
			}
			if idx < 0 {
				tb.Errorf("%s: statement has no plan expectation; add one to planCases:\n%s", pc.name, q.sql)
				continue
			}
			seen[idx] = true
			want := pc.queries[idx]
			plan, err := explain(ctx, plain, q)
			if err != nil {
				tb.Errorf("%s: explain: %v\n%s", pc.name, err, q.sql)
				continue
			}
			text := renderPlan(plan)
			if *dumpPlans {
				tb.Logf("%s\n%s\n%s", pc.name, normalizeSQL(q.sql), text)
			}
			for _, ix := range want.indexes {
				if !strings.Contains(text, " "+ix+" ") && !strings.Contains(text, " "+ix+"\n") && !strings.Contains(text, " "+ix+" (") {
					tb.Errorf("%s: plan does not use %s for\n%s\nactual plan:\n%s", pc.name, ix, q.sql, text)
				}
			}
			scanned := false
			for _, l := range plan {
				if fullScan.MatchString(l.detail) {
					scanned = true
				}
			}
			switch {
			case scanned && want.fullScanReason == "":
				tb.Errorf("%s: plan scans wasm_claims or contract_versions in full for\n%s\nactual plan:\n%s", pc.name, q.sql, text)
			case !scanned && want.fullScanReason != "":
				tb.Errorf("%s: documented full scan no longer happens; remove fullScanReason for %q\nactual plan:\n%s", pc.name, want.match, text)
			}
		}
		for i, want := range pc.queries {
			if !seen[i] {
				tb.Errorf("%s: expected statement %q was not run; update planCases", pc.name, want.match)
			}
		}
	}
}

// planFixture is the plan tests' database. Plans depend on ANALYZE
// statistics, so every test builds it the same way: the deterministic
// builder, then ANALYZE, with production indexes only.
var planFixture = catalogFixture{Hashes: 2000, Contracts: 2000}

func TestQueryPlansUseIntendedIndexes(t *testing.T) {
	ctx := context.Background()
	ro, in, path := openFixture(t, planFixture, "")
	checkPlans(ctx, t, path, ro.DB, in)
}

func TestQueryPlanCheckerFailsOnFullScan(t *testing.T) {
	ctx := context.Background()
	ro, _, _ := openFixture(t, planFixture, "")
	plan, err := explain(ctx, ro.DB, tracedQuery{sql: `SELECT count(*) FROM wasm_claims wc WHERE wc.raw_token = ?`, args: []any{"41"}})
	if err != nil {
		t.Fatal(err)
	}
	scanned := false
	for _, l := range plan {
		scanned = scanned || fullScan.MatchString(l.detail)
	}
	if !scanned {
		t.Fatalf("an unindexed predicate on wasm_claims should be reported as a full scan; plan:\n%s", renderPlan(plan))
	}
}
