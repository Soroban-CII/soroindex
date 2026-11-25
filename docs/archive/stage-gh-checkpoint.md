## STOP G/H report

Stages G and H are implemented. This report was recorded on 9 October 2026.
Review is required before Stage I under CLAUDE.md §0 and §7. Nothing in Stage I
or the deferred verifier stage has started.

Commits: implementation and documentation since STOP F, in order (the report
commit follows this list):

```text
9ec6083 feat(api): add read-only server and health endpoint
87e4f2f feat(api): add paginated contract filters and alias
d9501c7 feat(api): expose fixed contract details and historical claims
963115b feat(api): expose wasm parsing evidence and current users
108284e feat(api): count live contracts by sep and trust tier
2142a48 feat(api): report adoption gap anomalies and sync progress
1553c3f fix(api): paginate history before loading version claims
180f392 feat(api): enforce bounded per-ip request windows
f29d1bf fix(api): align verified filters and expose inferred provenance
94cac27 feat(cli): query contract tiers with json and csv output
a1524f0 feat(cli): show contract details and historical tier evidence
6687d39 feat(cli): inspect wasm metadata claims and matching evidence
69b78e5 feat(cli): report adoption and last indexed ledger
12ceb16 feat(cli): serve read-only api with graceful shutdown
0afd72e perf(store): measure sep queries over one million claims
fa8c45d fix(cli): include wasm command on native architectures
abe8649 build(repo): pin go 1.27.2 to fix standard library advisories
106bd69 build(repo): patch transitive compression vulnerability
5ed884d test(api): cover paginated users and unresolved references
930553d docs(store): record benchmark with patched go toolchain
7d90e4a build(repo): package static binary in nonroot distroless image
8942aff ci(repo): scan vulnerabilities and review dependency changes
999d0d8 ci(sync): follow testnet nightly and on demand
74d91fd docs(api): replace planned surface with real testnet captures
4fe14be docs(cli): document implemented query and serving commands
e133208 docs(repo): document validated nonroot docker setup
a1c6b71 docs(repo): update built status after api and hardening stages
```

Commands run (real output, trimmed only with "..."):

### Offline validation

```text
$ go test -count=1 ./...
ok  	github.com/Soroban-CII/soroindex/cmd/sep47idx	0.052s
ok  	github.com/Soroban-CII/soroindex/internal/api	0.052s
ok  	github.com/Soroban-CII/soroindex/internal/claims	0.002s
ok  	github.com/Soroban-CII/soroindex/internal/config	0.002s
ok  	github.com/Soroban-CII/soroindex/internal/ingest	0.276s
ok  	github.com/Soroban-CII/soroindex/internal/match	0.004s
ok  	github.com/Soroban-CII/soroindex/internal/phase0	0.051s
ok  	github.com/Soroban-CII/soroindex/internal/rpc	0.480s
ok  	github.com/Soroban-CII/soroindex/internal/store	0.072s
ok  	github.com/Soroban-CII/soroindex/internal/sync	0.015s
?   	github.com/Soroban-CII/soroindex/migrations	[no test files]
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	0.330s
?   	github.com/Soroban-CII/soroindex/rules	[no test files]
?   	github.com/Soroban-CII/soroindex/scripts/fixturetool	[no test files]

$ CGO_ENABLED=1 go test -race -count=1 ./...
ok  	github.com/Soroban-CII/soroindex/cmd/sep47idx	2.219s
ok  	github.com/Soroban-CII/soroindex/internal/api	2.415s
ok  	github.com/Soroban-CII/soroindex/internal/claims	1.010s
ok  	github.com/Soroban-CII/soroindex/internal/config	1.019s
ok  	github.com/Soroban-CII/soroindex/internal/ingest	6.736s
ok  	github.com/Soroban-CII/soroindex/internal/match	1.020s
ok  	github.com/Soroban-CII/soroindex/internal/phase0	1.865s
ok  	github.com/Soroban-CII/soroindex/internal/rpc	1.828s
ok  	github.com/Soroban-CII/soroindex/internal/store	2.627s
ok  	github.com/Soroban-CII/soroindex/internal/sync	1.422s
?   	github.com/Soroban-CII/soroindex/migrations	[no test files]
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	2.199s
?   	github.com/Soroban-CII/soroindex/rules	[no test files]
?   	github.com/Soroban-CII/soroindex/scripts/fixturetool	[no test files]

$ CGO_ENABLED=1 go test -race -count=1 ./internal/api
ok  	github.com/Soroban-CII/soroindex/internal/api	1.852s

$ golangci-lint run ./...
0 issues.

$ go mod verify
all modules verified
$ go mod tidy -diff
$ go vet ./...
```

`tidy -diff` and vet completed without diagnostics. The setup script ran and
was repeated after the patch updates; both executions passed test, build, lint
and the strict docs build.

### Query benchmark

```text
$ go test ./internal/store -run '^$' -bench '^BenchmarkContractsMillionClaims$' -benchtime=100x -count=1 -v
goos: linux
goarch: amd64
pkg: github.com/Soroban-CII/soroindex/internal/store
cpu: AMD EPYC 9V74 80-Core Processor                
BenchmarkContractsMillionClaims
    catalog_benchmark_test.go:83: fixture: 1000000 claims, 100000 live contracts, 1000 SEP-41 hashes; production indexes, read-only query, limit 50
BenchmarkContractsMillionClaims-4   	     100	  11789639 ns/op	         9.302 p50-ms	        23.78 p95-ms
PASS
ok  	github.com/Soroban-CII/soroindex/internal/store	14.867s
```

The [benchmark report](query-benchmark.md) records fixture construction,
cursor distribution, initial results and the concurrent-build rerun. p95 did
not exceed 200 ms; no schema change or materialization was made.

### Security

```text
$ govulncheck -db file:///workspace/scratch/stage-g/vulndb/data/osv ./...
No vulnerabilities found.
```

The default live database request failed with `Forbidden` in this cloud network.
The completed scan used unmodified official golang/vulndb OSV files from snapshot
`f5aaa67f6ede4dff5b467dd6beba251f3c706b05`. Go 1.27.2 and the transitive
compression update to 1.18.7 removed the reported findings. The
[dependency review](dependency-review.md) records the initial findings and module
licenses. CI uses the live database; no hosted run is claimed.

### Static targets

Each target was built with `CGO_ENABLED=0 go build -trimpath`:

```text
linux/amd64 CGO_ENABLED=0 PASS
linux/arm64 CGO_ENABLED=0 PASS
darwin/amd64 CGO_ENABLED=0 PASS
darwin/arm64 CGO_ENABLED=0 PASS
```

### Fuzz smoke

`make fuzz-smoke` completed all four targets. Output trimmed with `...`:

```text
== ./pkg/sepmeta FuzzReadCustomSections (30s)
...
...
PASS
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	31.024s
== ./pkg/sepmeta FuzzDecodeMeta (30s)
...
...
PASS
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	31.024s
== ./pkg/sepmeta FuzzDecodeSpec (30s)
...
...
PASS
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	31.025s
== ./pkg/sepmeta FuzzParseSEP47 (30s)
...
...
PASS
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	31.013s
```

These were 30-second smoke checks, not replacement claims for Stage B's recorded
10-minute runs. The smoke run used Go 1.27.1 before the security patch; parser
code did not change. The patched full parser tests and race tests passed.

### Real testnet and container validation

```text
$ /workspace/scratch/stage-g/sep47idx phase0 --network testnet --sample 200 --out /workspace/scratch/stage-g/census
...
testnet: 200 contracts (6 SAC, 185 wasm, 0 wasm_ref); 107 hashes fetched, 2 archived; declares any SEP: 0/107 hashes (0.00%), 0/183 contracts (0.00%); SEP-41 match 7 hashes; undeclared gap 7 hashes
wrote /workspace/scratch/stage-g/census
$ /workspace/scratch/stage-g/sep47idx sync --network testnet --db /workspace/scratch/stage-g/testnet.db --seed /workspace/scratch/stage-g/census/testnet-contracts.csv
...
seeded 200 of 200 contracts (0 not found, 9 archived, 0 hash hints differed); fetched 111 wasm; last_ledger 5097816
$ docker run --rm --read-only soroindex:stage-gh version
sep47idx dev (go1.27.2, linux/amd64)
$ docker inspect --format 'exit={{.State.ExitCode}} user={{.Config.User}} read_only={{.HostConfig.ReadonlyRootfs}}' soroindex-stage-gh
exit=0 user=65532:65532 read_only=true
```

Docker build completed with digest-pinned Go and distroless images. Its module
verification reported `all modules verified`. This cloud needed proxy build args,
a proxy hostname mapping, and the trusted CA bundle mounted as a BuildKit secret;
TLS was not bypassed. The extracted container binary was statically linked.
Read-only container stats returned 200 stored contracts, and HTTP health read a
fresh real tip. SIGTERM produced exit 0. The container was then removed.

The [API reference](api.md) includes executed curl requests and actual responses
for every endpoint, alias and continuation. The [CLI reference](cli.md) includes
real output for the new commands. This is a static sample; health lag grows
without a sync writer. No hosted API, image publication or release is claimed.

### Documentation and workflow checks

```text
$ .venv/bin/mkdocs build --strict
...
INFO    -  Documentation built in 0.36 seconds
$ go test -tags integration -run '^$' ./internal/sync
ok      github.com/Soroban-CII/soroindex/internal/sync    0.006s [no tests to run]
```

The latter only compiled the integration-tagged test. Stage F's unchanged
30-minute live test already passed with final lag 1; it was not repeated or
relabelled as a new hosted run. `integration.yml` has manual/nightly triggers,
a 40-minute job budget and retained logs. YAML syntax and trigger checks passed
locally. Action SHA pins were checked against upstream tags.

Tests added:

- `cmd/sep47idx/cmd_contract_test.go`: `TestContractCLIPositionalFlagsAndNotFound`
- `cmd/sep47idx/cmd_query_test.go`: `TestQueryCLIJSONCSVAndErrors`
- `cmd/sep47idx/cmd_serve_test.go`: `TestServeHTTPReadsIndexAndShutsDown`, `TestServeCLIRejectsInvalidConfiguration`
- `cmd/sep47idx/cmd_stats_test.go`: `TestStatsCLIStoredSnapshot`
- `cmd/sep47idx/cmd_wasm_detail_test.go`: `TestWasmCLIJSONAndValidation`
- `internal/api/catalog_test.go`: `TestContractsFiltersPaginationAndAlias`
- `internal/api/details_test.go`: `TestContractFixedShapeAndHistory`
- `internal/api/history_test.go`: `TestHistoryPaginationAndStaleVerification`
- `internal/api/limit_test.go`: `TestPerIPRateLimitIgnoresForwardedHeaders`
- `internal/api/references_test.go`: `TestWasmUserPaginationAndExecutableReferences`
- `internal/api/seps_test.go`: `TestSEPsTierCountsAndPagination`
- `internal/api/server_test.go`: `TestHealthReportsFreshTipAndReadOnlyStore`, `TestServerRoutingAndMissingProgress`
- `internal/api/stats_test.go`: `TestStatsAdoptionAndGap`
- `internal/api/verified_test.go`: `TestLatestFailedVerificationCannotMatchOlderPass`
- `internal/api/wasm_test.go`: `TestWasmEvidenceAndBadInput`
- `internal/store/catalog_benchmark_test.go`: `BenchmarkContractsMillionClaims`
- `cmd/sep47idx/main_test.go`: `TestAllBuiltCommandsAreRegistered`

Claims that need evidence:

- Every API endpoint works: named handler tests above and real testnet captures
  in `api.md`. Aggregate endpoints return empty collections rather than false 404s.
- Tiers stay independent: `TestContractsFiltersPaginationAndAlias`,
  `TestLatestFailedVerificationCannotMatchOlderPass`, and
  `TestHistoryPaginationAndStaleVerification`. SACs stay protocol; old passes
  cannot satisfy current filters and a latest failure cannot be overridden by
  an earlier tool's pass.
- Fixed detail shape remains intact: `TestContractFixedShapeAndHistory` checks
  its ten fields. `TestWasmUserPaginationAndExecutableReferences` covers archival,
  reference fields and null unresolved hashes without fabricated claims.
- Native CLI registers all commands: the real CLI run exposed the `_wasm.go`
  architecture suffix mistake. `TestAllBuiltCommandsAreRegistered` now guards
  registration, and renamed files execute `TestWasmCLIJSONAndValidation` on Linux.
  CLI tests prove JSON/CSV and exit codes.
- Serving is read-only: API construction rejects a writable store; the container
  worked with a read-only root and database bind mount. Its extracted binary is
  static and image configuration uses UID/GID 65532.
- Query p95 is below 200 ms: the production-schema million-row fixture and
  percentile output above. This is a local warm-cache result, not a load guarantee.
- Security checks are clean: gosec-enabled lint and official snapshot vulnerability
  output above. The live database domain remains pending environment publication.
- Cloud setup is reusable in this instance: tested and repeated install script;
  saved `install_script`, `start_skill`, and an additive `vuln.go.dev` network entry.
  Saving the draft does not apply or publish it, and a fresh-task restoration has
  not been tested. Review/save settings and publish to activate the draft.

Contradictions found in CLAUDE.md or upstream sources:

- The old build pin was behind Go's current patch and had security findings.
  Per §3's registry-refresh rule, the pin is now Go 1.27.2; the floor stays 1.26.0.
- §8's blanket "pagination, bad input and 404" wording cannot apply literally
  to singleton health/stats responses. Pagination applies to contract lists,
  history, Wasm users and SEP counts. Resource absence and unknown paths return
  404; valid empty collections return 200. The fixed contract detail is unchanged.
- No new trust-definition or database-schema contradiction was found. The operator's
  Stage F fixture deployment/upgrade skip remains a skip, not a passing live check.

Next stage: I. First planned commit: refresh the Wave application against the
built API, CLI, measured evidence and remaining publication work. Wait for the
operator's go-ahead before starting it.
