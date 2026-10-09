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
