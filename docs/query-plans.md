# Query-plan regression tests

Most API queries in the [query benchmark](query-benchmark.md) stay fast because
they reach their rows through an index. A refactor that loses an index would pass every
functional test and only show up as slow production queries. The plan tests
catch it in `go test`.

## What they check

`TestQueryPlansUseIntendedIndexes` in `internal/store/queryplan_test.go`:

1. Builds the deterministic fixture (`catalogFixture` in
   `internal/store/fixture_test.go`: 20,000 claims, 2,000 contracts, every
   contract kind, closed versions, matches under the current and a stale
   ruleset, verifications and anomalies) with the production migrations, then
   runs `ANALYZE`. Plans depend on those statistics, so every run builds the
   database the same way.
2. Calls the real `Store` methods behind each endpoint through a tracing
   SQLite driver, which records every statement and its arguments.
3. Runs `EXPLAIN QUERY PLAN` on each recorded statement with its arguments.
4. Fails if:
   - a statement's plan does not use every index listed for it;
   - any plan step reads `wasm_claims` or `contract_versions` in full
     (`SCAN wasm_claims`, `SCAN wc`, `SCAN contract_versions`) without a
     documented exception;
   - a method runs a statement with no expectation (so new SQL is covered
     as soon as it is added), or an expected statement is no longer run.

Every failure prints the statement and the actual plan.

The 10M-claim benchmark (`make bench-10m`) runs the same assertions on its
fixture before timing anything, so the plans hold at 1M contracts too.

## Coverage

| API / CLI | Store call | Statements | Indexes asserted |
| --- | --- | --- | --- |
| `GET /v1/contracts?implements=41` (declared) | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `idx_claims_sep` |
| `GET /v1/contracts` (declared, no SEP) | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `sqlite_autoindex_wasm_claims_1` |
| `implements=41,42` (AND) | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `idx_claims_sep` |
| `implements_any=41,951` (OR) | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `idx_claims_sep` |
| `implements=41&kind=wasm_ref&cursor=…` | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `idx_claims_sep` |
| `tier=inferred` | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `sqlite_autoindex_interface_matches_1` |
| `tier=verified` | `Contracts` | 1 | `sqlite_autoindex_contracts_1`, `sqlite_autoindex_verifications_1`, `idx_claims_sep` |
| `tier=protocol` | `Contracts` | 1 | `sqlite_autoindex_contracts_1` |
| `GET /v1/contracts/{id}`, `sep47idx contract` | `Contract` | 6 | contracts, sync_state, contract_versions, wasm_claims, interface_matches and verifications primary keys |
| `GET /v1/contracts/{id}/history`, `contract --history` | `HistoryPage` | 6 per version | the same primary keys |
| `GET /v1/wasm/{hash}`, `sep47idx wasm` | `Wasm` | 6 | wasm, sync_state, wasm_claims, parse_anomalies and interface_matches primary keys, `idx_contracts_hash` |
| `GET /v1/seps` | `SEPs` | 2 | `sqlite_autoindex_wasm_claims_1`, `idx_matches_sep`, `idx_contracts_hash`, `sqlite_autoindex_verifications_1`, `idx_claims_sep` |
| `GET /v1/stats`, `sep47idx stats` | `Stats` | 14 | `sqlite_autoindex_wasm_1`, `sqlite_autoindex_wasm_claims_1`, `idx_matches_sep`, `idx_contracts_hash` where each applies |
| `sep47idx gap` | `Gap` | 1 | `idx_matches_sep`, `idx_contracts_hash`, `sqlite_autoindex_wasm_claims_1` |

The live-contract counts in `Stats` and the `SEPs` aggregation read every
`contracts` row by design; only `wasm_claims` and `contract_versions` are
forbidden from full scans.

## The one documented exception

`Stats` counts anomalies with
`SELECT anomaly FROM parse_anomalies UNION ALL SELECT anomaly FROM wasm_claims WHERE anomaly IS NOT NULL`.
No index covers `wasm_claims.anomaly`, so this reads all of `wasm_claims`:

```text
CO-ROUTINE (subquery-2)
  COMPOUND QUERY
    LEFT-MOST SUBQUERY
      SCAN parse_anomalies
    UNION ALL
      SCAN wasm_claims
SCAN (subquery-2)
USE TEMP B-TREE FOR GROUP BY
```

Removing the scan needs an index on `wasm_claims (anomaly)`, which is a
schema change and needs maintainer approval. The test records the exception
with its reason and also fails if the scan disappears, so the exception
cannot spread to other statements or outlive a fix unnoticed. The
[benchmark decision record](query-benchmark.md#decision-record) measures what
this and the other aggregate queries cost at 10M claims.

## Running and updating

```sh
go test ./internal/store -run 'TestQueryPlan' -v
```

Print every statement and its plan:

```sh
go test ./internal/store -run TestQueryPlansUseIntendedIndexes -v -args -dump-plans
```

When a change legitimately alters a plan, update `planCases` in
`internal/store/queryplan_test.go` in the same commit and say in the commit
body why the new plan is at least as good, with `-dump-plans` output before
and after. Never add a `fullScanReason` to make a test pass without a
maintainer agreeing to the scan.

Checked on 20 November 2026: changing the catalog predicate to
`wc.sep + 0 = ?`, which defeats `idx_claims_sep`, made the test fail with
`plan does not use idx_claims_sep` and the actual plan
(`SEARCH wc EXISTS USING COVERING INDEX sqlite_autoindex_wasm_claims_1 (wasm_hash=?)`).
The change was reverted.
