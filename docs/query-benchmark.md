# Million-claim query benchmark

Measured on 9 October 2026 in the cloud development environment. Go 1.27.1,
linux/amd64, AMD EPYC 9V74, `GOMAXPROCS=4`. The synthetic database uses the
production schema and indexes. No migration, index, or materialized table was added.

The fixture has 1,000,000 declaration rows on 100,000 Wasm hashes and 100,000 live
contracts. Each hash declares ten SEPs; 1,000 hashes declare SEP-41. This measures
the read-only `Store.Contracts` query behind `GET /v1/contracts?implements=41`,
including row decoding and continuation encoding. Each page returns 50 contracts.

Five pages warm the connection and cache. The measured 100 pages use deterministic
cursor positions throughout the first 80% of sorted contract IDs, leaving enough
results for full pages. Percentiles cover query calls, not fixture creation. This
is a local warm-cache benchmark, not a concurrency or cold-disk latency guarantee.

```text
$ go test ./internal/store -run '^$' -bench '^BenchmarkContractsMillionClaims$' -benchtime=100x -count=1 -v
goos: linux
goarch: amd64
pkg: github.com/Soroban-CII/soroindex/internal/store
cpu: AMD EPYC 9V74 80-Core Processor
BenchmarkContractsMillionClaims
    catalog_benchmark_test.go:83: fixture: 1000000 claims, 100000 live contracts, 1000 SEP-41 hashes; production indexes, read-only query, limit 50
BenchmarkContractsMillionClaims-4          100       5247496 ns/op             5.046 p50-ms             6.773 p95-ms
PASS
ok      github.com/Soroban-CII/soroindex/internal/store    10.629s
```

p95 is below the 200 ms Stage G threshold. The `claims` view remains a view;
materialization is unnecessary. The benchmark rebuilds its fixture in a temporary
directory and checks the actual row counts before measuring.

## Patched toolchain rerun

After updating to Go 1.27.2 and `klauspost/compress` 1.18.7, the same command ran
again on 9 October 2026. A Docker build and race checks were running on the same
machine, so this is not a controlled compiler comparison.

```text
goos: linux
goarch: amd64
pkg: github.com/Soroban-CII/soroindex/internal/store
cpu: AMD EPYC 9V74 80-Core Processor
BenchmarkContractsMillionClaims
    catalog_benchmark_test.go:83: fixture: 1000000 claims, 100000 live contracts, 1000 SEP-41 hashes; production indexes, read-only query, limit 50
BenchmarkContractsMillionClaims-4          100      11789639 ns/op             9.302 p50-ms             23.78 p95-ms
PASS
ok      github.com/Soroban-CII/soroindex/internal/store    14.867s
```

The patched build also stays below the materialization threshold.

## Every API query at 10M claims (20 November 2026)

Issue #99 extended the benchmark to every query behind the API and to 10 million
claim rows. The earlier sections timed only `implements=41`.

**Machine.** Apple M4 (10 CPUs), darwin/arm64, Go 1.27.4 (`toolchain go1.27.4`),
modernc.org/sqlite v1.60.1. A local warm-cache run on a laptop SSD with no other
benchmark running. It is not a concurrency or cold-disk measurement.

**Fixture.** `catalogFixture` in `internal/store/fixture_test.go` is deterministic:
no random data, so two builds of one size are identical. It uses the production
migrations and indexes only, then runs `ANALYZE`. The shape, documented in the
file:

- every declaring hash declares ten SEPs, and each SEP is on 1% of hashes;
- 1% more hashes declare nothing and match SEP-41 (the undeclared gap);
- 2% of contracts are SACs, 1% are `wasm_ref`, and 1% are archived;
- 10% of contracts have a closed earlier version;
- some contracts have passing and failing verifications, and some hashes have
  anomalies.

Every hash declaring ten SEPs is far denser than mainnet today (the 7 October
2026 census found 0.60% of live hashes declaring any SEP). It is a stress shape,
not a forecast.

| Size | Claims | Contracts | Wasm hashes | Versions | Matches | Database file |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `make bench` | 1,000,000 | 100,000 | 101,000 | 110,000 | 111,100 | 389,296,128 bytes |
| `make bench-10m` | 10,000,000 | 1,000,000 | 1,010,000 | 1,100,000 | 1,111,000 | 3,897,176,064 bytes |

**One command.** `make bench-10m` builds the fixture and runs
`BenchmarkAPIQueries` and `BenchmarkMaterializedDesign` with 10 timed
iterations per query, after three warm-up calls. `SEP47IDX_BENCH_DB=/path/x.db`
keeps the fixture for reuse; the builder checks its row counts every run. The
10M fixture took 2m32s to build. Before timing, the benchmark asserts the
[query-plan expectations](query-plans.md) on the same database, and they held
at both sizes.

```text
$ SEP47IDX_BENCH_DB=$S/bench10m.db make bench-10m
SEP47IDX_BENCH_HASHES=1000000 SEP47IDX_BENCH_CONTRACTS=1000000 go test ./internal/store -run '^$' -bench '^Benchmark(APIQueries|MaterializedDesign)$' -benchtime=10x -count=1 -timeout 3h -v
goos: darwin
goarch: arm64
pkg: github.com/Soroban-CII/soroindex/internal/store
cpu: Apple M4
BenchmarkAPIQueries
    api_benchmark_test.go:134: go go1.27.4 darwin/arm64, 10 CPUs; date 2026-11-20
    api_benchmark_test.go:135: fixture: 10000000 claims, 1000000 contracts, 1010000 wasm hashes, 1100000 contract_versions, 1111000 interface_matches, 10000 SEP-41 hashes; db 3897176064 bytes; ready in 36s
BenchmarkAPIQueries/contracts/declared/implements=41-10         	      10	  15311388 ns/op	        16.89 p50-ms	        23.03 p95-ms
BenchmarkAPIQueries/contracts/declared/implements=1001-none-10  	      10	 501359404 ns/op	       226.2 p50-ms	      1293 p95-ms
...
BenchmarkAPIQueries/seps-10                                                   	      10	87942626846 ns/op	     87538 p50-ms	     89520 p95-ms
BenchmarkAPIQueries/stats-10                                                  	      10	111123511033 ns/op	    109448 p50-ms	    118467 p95-ms
...
BenchmarkMaterializedDesign
    materialized_benchmark_test.go:143: full refresh 5m35.436s (current_claims 2m54.519s, sep_counts 33.448s, stat_values 2m7.469s); current_claims 9700000 rows; materialized file 703807488 bytes
...
PASS
ok  	github.com/Soroban-CII/soroindex/internal/store	3282.257s
```

("ready in 36s" is the reused fixture's open and count check.) The full output
of both runs gave these percentiles, in milliseconds:

| Query (Store call behind the endpoint) | 1M p50 | 1M p95 | 10M p50 | 10M p95 |
| --- | ---: | ---: | ---: | ---: |
| `/v1/contracts?implements=41` | 11.97 | 12.31 | 16.89 | 23.03 |
| `/v1/contracts?implements=1001` (no contract declares it) | 23.15 | 23.36 | 226.2 | **1,293** |
| `/v1/contracts` (declared, no SEP) | 0.20 | 0.21 | 1.11 | 2.09 |
| `/v1/contracts?implements=41,42` | 7.58 | 7.78 | 5.92 | 6.24 |
| `/v1/contracts?implements_any=41,951` | 18.39 | 22.49 | 6.34 | 6.53 |
| `/v1/contracts?implements=41&kind=wasm_ref` | 12.58 | 19.44 | 566.2 | **1,436** |
| `/v1/contracts?tier=inferred&implements=41` | 20.90 | 22.20 | 16.83 | 37.35 |
| `/v1/contracts?tier=verified&implements=41` | 2.90 | 3.04 | 3.92 | 4.58 |
| `/v1/contracts?tier=protocol&implements=41` | 0.51 | 0.53 | 0.48 | 0.49 |
| `/v1/contracts/{id}` | 0.11 | 0.11 | 0.26 | 0.35 |
| `/v1/contracts/{id}/history` | 0.10 | 0.11 | 0.09 | 0.14 |
| `/v1/wasm/{hash}` | 0.11 | 0.14 | 0.40 | 1.10 |
| `/v1/seps` | 3,966 | **4,327** | 87,538 | **89,520** |
| `/v1/stats` | 1,243 | **1,350** | 109,448 | **118,467** |
| `sep47idx gap --sep 41` | 8.10 | 8.47 | 111.4 | 129.3 |

Bold marks a p95 over the 200 ms threshold. Ten samples per query make p95 the
ninth-slowest call; treat it as a rough tail, not a precise one.

## Decision record

**Question (issue #99).** Should the `claims` view be materialized at 10M
claims and 1M contracts?

**Measured answer.**

1. **Not for the reason it was proposed.** No API query reads the `claims`
   view. The query it was meant to speed up, `implements=41`, has a p95 of
   23.03 ms at 10M, well under 200 ms, because the keyset scan finds a match in
   about every hundredth contract.
2. **Four queries exceed 200 ms at 10M, and two of them already do at 1M:**
   - `/v1/seps` and `/v1/stats` aggregate every live contract on every call:
     89.5 s and 118.5 s p95 at 10M, and 4.3 s and 1.35 s at 1M.
   - Catalog filters that match few contracts scan the whole contract keyspace
     to fill a page: 1.29 s for a SEP no contract declares, and 1.44 s for
     `implements=41&kind=wasm_ref`.
3. **A materialized design fixes the reads.** `BenchmarkMaterializedDesign`
   built it in a scratch file attached to the same fixture, so no production
   schema changed:
   - `current_claims (sep, contract_id)`, the claims view restricted to live,
     current, non-SAC contracts;
   - `sep_counts`, the four tier counts per SEP;
   - `stat_values`, the `/v1/stats` numbers.

   | Read | Before, 10M p95 | Materialized, 10M p95 | Before, 1M p95 | Materialized, 1M p95 |
   | --- | ---: | ---: | ---: | ---: |
   | `/v1/seps` (first page) | 89,520 | 0.026 | 4,327 | 0.028 |
   | `/v1/stats` | 118,467 | 0.010 | 1,350 | 0.011 |
   | `implements=1001` (no matches) | 1,293 | 0.025 | 23.36 | 0.026 |
   | `implements=41` | 23.03 | 18.90 | 12.31 | 0.234 |

4. **It moves the cost to sync.**
   - A full rebuild took 5m35s at 10M (15.1 s at 1M) and adds 703,807,488 bytes
     (70,352,896 at 1M). That is far too slow to run after each sync batch.
   - Maintaining the declared tier incrementally inside the sync transaction
     cost 13.57 ms p50 and 16.99 ms p95 per upgraded contract at 10M (0.26 and
     0.29 ms at 1M). The step drops the old hash's rows, adds the new hash's and
     moves the counts.
   - Not measured: incremental upkeep of the inferred, verified and protocol
     counts and of `stat_values`, a materialized `kind` filter, and a
     correctness check of incremental against full recomputation. The
     experiment measures cost, not a finished implementation.

**Decision.** Materialize, but not the `claims` view as such. The measured case
is for summary tables (`sep_counts`, `stat_values`) and a current-claims table
keyed by `(sep, contract_id)`, all maintained incrementally in the sync
transaction. Together with the `wasm_claims (anomaly)` index the
[plan tests](query-plans.md#the-one-documented-exception) call for, this is a
new migration, which needs maintainer approval before any code is written
(CLAUDE.md §5.8, CONTRIBUTING.md). **This change ships no schema change.** It
ships the benchmark, the fixture builder and this record, so the design can be
reviewed against numbers. Until a design is approved, `/v1/seps` and
`/v1/stats` should be treated as unsuitable for public traffic on an index
approaching this size.
