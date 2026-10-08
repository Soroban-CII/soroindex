# Stage F progress — 8 October 2026

Steps 23–26 are implemented and validated offline. Step 27 has a runnable integration test but is blocked by network access. Step 28 needs the operator's deployment and upgrade. This is not a completed STOP F checkpoint; Stage G has not started.

## Commits

```text
33d427b feat(ingest): preserve ordered contract changes for incremental sync
ca6e630 fix(store): prevent older replays from regressing current code
f93c1e6 feat(ingest): apply ordered ledger changes and reference upgrades
e4a5172 feat(sync): add atomic batching resume and retention checks
4ba6e6d feat(cli): expose incremental sync start resume and follow modes
519a27a test(sync): add thirty-minute testnet follow validation
a7d85ad fix(cli): return success when showing sync help
```

## Required regression checks

CLAUDE.md calls these “five” tests in the stage list, but section 5.9 enumerates seven. All seven ran and passed:

```text
$ go test -count=1 -v -run '^(TestLedgerReplayIdenticalDatabase|TestCrashMidBatchRollsBackAndResumes|TestInstanceUpgradeExactlyOneVersion|TestArchivedCodeKeepsClaimsAndRestores|TestRetentionGapWritesNothing|TestReferenceUpgradeOnlyReferencingContracts|TestUnresolvedReferenceClaimsNothing)$' ./internal/ingest ./internal/sync
=== RUN   TestLedgerReplayIdenticalDatabase
--- PASS: TestLedgerReplayIdenticalDatabase (0.01s)
=== RUN   TestInstanceUpgradeExactlyOneVersion
--- PASS: TestInstanceUpgradeExactlyOneVersion (0.00s)
=== RUN   TestArchivedCodeKeepsClaimsAndRestores
--- PASS: TestArchivedCodeKeepsClaimsAndRestores (0.00s)
=== RUN   TestReferenceUpgradeOnlyReferencingContracts
--- PASS: TestReferenceUpgradeOnlyReferencingContracts (0.00s)
=== RUN   TestUnresolvedReferenceClaimsNothing
--- PASS: TestUnresolvedReferenceClaimsNothing (0.00s)
PASS
ok  	github.com/Soroban-CII/soroindex/internal/ingest	0.021s
=== RUN   TestCrashMidBatchRollsBackAndResumes
--- PASS: TestCrashMidBatchRollsBackAndResumes (0.00s)
=== RUN   TestRetentionGapWritesNothing
=== RUN   TestRetentionGapWritesNothing/fresh
=== RUN   TestRetentionGapWritesNothing/resume
--- PASS: TestRetentionGapWritesNothing (0.00s)
    --- PASS: TestRetentionGapWritesNothing/fresh (0.00s)
    --- PASS: TestRetentionGapWritesNothing/resume (0.00s)
PASS
ok  	github.com/Soroban-CII/soroindex/internal/sync	0.009s
```

The source for replay, upgrades, archival, reference fan-out and unresolved references is [apply_test.go](https://github.com/ciscokwiz/soroindex/blob/f93c1e6/internal/ingest/apply_test.go). Crash rollback/resume and retention-gap tests are in [loop_test.go](https://github.com/ciscokwiz/soroindex/blob/e4a5172/internal/sync/loop_test.go). Additional tests cover future-state reference lookups, reference archival/restoration, fresh-tip confirmation, follow cancellation and the CLI's startup/resume path.

## Full race suite

```text
$ CGO_ENABLED=1 go test -race -count=1 ./...
ok  	github.com/Soroban-CII/soroindex/cmd/sep47idx	1.662s
ok  	github.com/Soroban-CII/soroindex/internal/claims	1.022s
ok  	github.com/Soroban-CII/soroindex/internal/config	1.037s
ok  	github.com/Soroban-CII/soroindex/internal/ingest	10.529s
ok  	github.com/Soroban-CII/soroindex/internal/match	1.085s
ok  	github.com/Soroban-CII/soroindex/internal/phase0	2.154s
ok  	github.com/Soroban-CII/soroindex/internal/rpc	2.097s
ok  	github.com/Soroban-CII/soroindex/internal/store	3.153s
ok  	github.com/Soroban-CII/soroindex/internal/sync	1.588s
?   	github.com/Soroban-CII/soroindex/migrations	[no test files]
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	3.846s
?   	github.com/Soroban-CII/soroindex/rules	[no test files]
?   	github.com/Soroban-CII/soroindex/scripts/fixturetool	[no test files]
```

The CLI's help exit handling was corrected after this race run; its CLI suite, rebuilt help command and full setup script subsequently passed.

## Other validation

- `golangci-lint run ./...` with v2.14.0: `0 issues.`
- `go vet ./...`: exit 0.
- `go mod tidy -diff`: exit 0, no diff.
- `make fuzz-smoke`: all four existing fuzz targets passed for 30 seconds each.
- Static `CGO_ENABLED=0` builds passed for linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.
- `make build`, the rebuilt CLI's help commands and the reusable installation script passed.
- `mkdocs build --strict`: exit 0 after checking the updated documentation.
- The database schema, trust tiers and public API specification were not changed.

## Live checks outstanding

```text
$ go test -tags integration -run '^TestFollowTestnet30Minutes$' -timeout 35m ./internal/sync
--- FAIL: TestFollowTestnet30Minutes (3.23s)
    integration_test.go:41: getNetwork: Post "https://soroban-testnet.stellar.org": Forbidden
FAIL
FAIL github.com/Soroban-CII/soroindex/internal/sync 3.236s
FAIL
```

This failure occurred before following any ledgers. It is a network-access blocker, not a completed 30-minute run. The draft now includes `soroban-testnet.stellar.org`, preserving the existing `storage.googleapis.com` entry. Saving the draft does not apply or publish it.

Step 28 requires the operator to deploy `token_full_sep.wasm` on testnet, start tracking that contract, then upgrade it to `token_partial.wasm`. Its contract ID and upgrade transaction/ledger will let us check the new version row and the changed inferred status. No deployment or keyed transaction was attempted.

Next: enable live testnet access, complete steps 27–28, then present the full STOP F evidence before Stage G.
