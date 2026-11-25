# STOP F report

9 October 2026. Stage F is ready for operator review. Step 28 was explicitly waived by the operator; it is skipped, not a successful live deployment/upgrade.

## Commits

Prepared since the onboarding checkout at `b95147d` (the existing ledger reader was already committed as `97e6b2d`):

```text
33d427b feat(ingest): preserve ordered contract changes for incremental sync
ca6e630 fix(store): prevent older replays from regressing current code
f93c1e6 feat(ingest): apply ordered ledger changes and reference upgrades
e4a5172 feat(sync): add atomic batching resume and retention checks
4ba6e6d feat(cli): expose incremental sync start resume and follow modes
519a27a test(sync): add thirty-minute testnet follow validation
a7d85ad fix(cli): return success when showing sync help
3401de6 docs(sync): record incremental workflow and stage f evidence
73a3713 docs(sync): record operator waiver of live fixture upgrade check
93c92c2 docs(sync): align constraints checklist with seven regression cases
```

## Commands run (real output)

```text
$ go test -v -count=1 -tags integration -run '^TestFollowTestnet30Minutes$' -timeout 35m ./internal/sync
=== RUN   TestFollowTestnet30Minutes
    integration_test.go:65: caught_up last=5097124 latest=5097124 lag=0
...
    integration_test.go:65: caught_up last=5097483 latest=5097483 lag=0
    integration_test.go:87: 30-minute follow passed: start=5097124 last=5097483 latest=5097484 lag=1
--- PASS: TestFollowTestnet30Minutes (1801.52s)
PASS
ok  	github.com/Soroban-CII/soroindex/internal/sync	1801.529s
```

```text
$ go test -count=1 -v -run '^(TestLedgerReplayIdenticalDatabase|TestCrashMidBatchRollsBackAndResumes|TestInstanceUpgradeExactlyOneVersion|TestArchivedCodeKeepsClaimsAndRestores|TestRetentionGapWritesNothing|TestReferenceUpgradeOnlyReferencingContracts|TestUnresolvedReferenceClaimsNothing)$' ./internal/ingest ./internal/sync
=== RUN   TestLedgerReplayIdenticalDatabase
--- PASS: TestLedgerReplayIdenticalDatabase (0.01s)
=== RUN   TestInstanceUpgradeExactlyOneVersion
--- PASS: TestInstanceUpgradeExactlyOneVersion (0.00s)
=== RUN   TestArchivedCodeKeepsClaimsAndRestores
--- PASS: TestArchivedCodeKeepsClaimsAndRestores (0.00s)
=== RUN   TestReferenceUpgradeOnlyReferencingContracts
--- PASS: TestReferenceUpgradeOnlyReferencingContracts (0.01s)
=== RUN   TestUnresolvedReferenceClaimsNothing
--- PASS: TestUnresolvedReferenceClaimsNothing (0.00s)
PASS
ok  	github.com/Soroban-CII/soroindex/internal/ingest	0.032s
=== RUN   TestCrashMidBatchRollsBackAndResumes
--- PASS: TestCrashMidBatchRollsBackAndResumes (0.00s)
=== RUN   TestRetentionGapWritesNothing
=== RUN   TestRetentionGapWritesNothing/fresh
=== RUN   TestRetentionGapWritesNothing/resume
--- PASS: TestRetentionGapWritesNothing (0.00s)
    --- PASS: TestRetentionGapWritesNothing/fresh (0.00s)
    --- PASS: TestRetentionGapWritesNothing/resume (0.00s)
PASS
ok  	github.com/Soroban-CII/soroindex/internal/sync	0.016s
```

Earlier validation, including the full race suite, linter, fuzz smoke and four static build targets, is recorded in the [Stage F progress report](stage-f-progress.md).

## Tests added

- `internal/ingest/apply_test.go`: ledger replay, direct upgrade, code archival/restoration, reference fan-out, unresolved references, malformed facts, reference archival/restoration and refusal to use future reference state.
- `internal/sync/loop_test.go`: crash rollback/resume, retention gaps, fresh-tip confirmation, first-run requirements and follow cancellation.
- `cmd/sep47idx/cmd_sync_test.go`: CLI startup/resume, retention-gap state preservation and invalid flags.
- `internal/sync/integration_test.go`: the real 30-minute testnet follow run, now passed.
- `internal/store/write_test.go`: regression checks preventing older contract/reference upserts from regressing current code.

## Claims that need evidence

| Claim | Evidence |
| --- | --- |
| Replaying ledgers preserves database contents | `TestLedgerReplayIdenticalDatabase` compares table dumps. |
| Cancellation after writes rolls back the batch and resume point | `TestCrashMidBatchRollsBackAndResumes` cancels after two writes, checks unchanged progress and no partial rows, then reruns successfully. |
| An instance upgrade closes the old version and opens exactly one new row | `TestInstanceUpgradeExactlyOneVersion` also checks the partial fixture's inferred status. |
| Code archival preserves claims | `TestArchivedCodeKeepsClaimsAndRestores`. |
| Retention gaps return the sentinel and write no progress | `TestRetentionGapWritesNothing`, plus the CLI regression. |
| A reference update upgrades only its referencing contracts | `TestReferenceUpgradeOnlyReferencingContracts`. |
| Unresolved references claim nothing | `TestUnresolvedReferenceClaimsNothing`. |
| Testnet follows for 30 minutes with no sync errors and final lag below ten | Live test passed in 1801.52 seconds; start 5097124, last 5097483, fresh latest 5097484, lag 1. |

## Step 28 evidence

**Operator-approved skip.** The operator stated they do not have a deployed fixture and asked to skip this check. This waiver is recorded in CLAUDE.md. No default contract ID was substituted and no deployment or upgrade transaction is claimed. Offline fixtures validate version rows and changed inferred status.

## Contradictions found in CLAUDE.md or upstream sources

The stage/checklist text said “five” tests while section 5.9 listed seven. Those counts now say seven. No upstream contradiction was found during this continuation. The network blocker from 8 October is resolved.

## Next stage

Stage G: first planned commit, `feat(api): add read-only server and health endpoint`. Await the operator's go-ahead at STOP F before implementing it, as required by CLAUDE.md section 0.

## Required test sources

## TestLedgerReplayIdenticalDatabase

Source: `internal/ingest/apply_test.go`

```go
func TestLedgerReplayIdenticalDatabase(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	facts := []ContractFacts{{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}}, {Ledger: 20, Changes: []Change{instanceChange(t, seedCID(1), h2, Updated)}}}
	applyFacts(t, ix, facts...)
	before := dump(t, s)
	applyFacts(t, ix, facts...)
	if after := dump(t, s); before != after {
		t.Fatalf("replay changed tables:\nbefore %s\nafter %s", before, after)
	}
}
```

## TestInstanceUpgradeExactlyOneVersion

Source: `internal/ingest/apply_test.go`

```go
func TestInstanceUpgradeExactlyOneVersion(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}}, ContractFacts{Ledger: 20, Changes: []Change{instanceChange(t, seedCID(1), h2, Updated)}})
	var count, closed int
	if err := s.DB.QueryRow(`SELECT count(*),sum(to_ledger = 20) FROM contract_versions`).Scan(&count, &closed); err != nil || count != 2 || closed != 1 {
		t.Fatalf("versions %d closed %d: %v", count, closed, err)
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM interface_matches WHERE wasm_hash = ?`, h2).Scan(&status); err != nil || status != "partial" {
		t.Fatalf("upgrade status %q: %v", status, err)
	}
}
```

## TestArchivedCodeKeepsClaimsAndRestores

Source: `internal/ingest/apply_test.go`

```go
func TestArchivedCodeKeepsClaimsAndRestores(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code}})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created)}})
	hash, _ := ParseWasmHash(h)
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{{Kind: Evicted, Key: &xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: hash}}}}})
	var claims, archived int
	if err := s.DB.QueryRow(`SELECT count(*) FROM wasm_claims WHERE wasm_hash = ?`, h).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("claims %d: %v", claims, err)
	}
	if err := s.DB.QueryRow(`SELECT archived FROM contracts`).Scan(&archived); err != nil || archived != 1 {
		t.Fatalf("archived %d: %v", archived, err)
	}
	applyFacts(t, ix, ContractFacts{Ledger: 21, Changes: []Change{{Kind: Restored, Entry: &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: hash, Code: code}}}}}})
	if err := s.DB.QueryRow(`SELECT archived FROM contracts`).Scan(&archived); err != nil || archived != 0 {
		t.Fatalf("restored %d: %v", archived, err)
	}
}
```

## TestReferenceUpgradeOnlyReferencingContracts

Source: `internal/ingest/apply_test.go`

```go
func TestReferenceUpgradeOnlyReferencingContracts(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}})
	owner := seedCID(9)
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{refChange(t, owner, "v1", h), referenceInstance(t, seedCID(1), owner, "v1"), referenceInstance(t, seedCID(2), owner, "v1"), instanceChange(t, seedCID(3), h, Created)}})
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{refChange(t, owner, "v1", h2)}})
	var changed, versions int
	if err := s.DB.QueryRow(`SELECT count(*) FROM contracts WHERE current_wasm_hash = ?`, h2).Scan(&changed); err != nil || changed != 2 {
		t.Fatalf("changed %d: %v", changed, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM contract_versions WHERE from_ledger = 20`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions %d: %v", versions, err)
	}
	before := dump(t, s)
	applyFacts(t, ix, ContractFacts{Ledger: 20, Changes: []Change{refChange(t, owner, "v1", h2)}})
	if dump(t, s) != before {
		t.Fatal("reference replay changed database")
	}
}
```

## TestUnresolvedReferenceClaimsNothing

Source: `internal/ingest/apply_test.go`

```go
func TestUnresolvedReferenceClaimsNothing(t *testing.T) {
	ix, s := newIndexer(t, &entryRPC{t: t, latest: 20})
	applyFacts(t, ix, ContractFacts{Ledger: 10, Changes: []Change{referenceInstance(t, seedCID(1), seedCID(9), "missing")}})
	var hash *string
	var count int
	if err := s.DB.QueryRow(`SELECT current_wasm_hash FROM contracts`).Scan(&hash); err != nil || hash != nil {
		t.Fatalf("hash %v: %v", hash, err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM claims WHERE current = 1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claims %d: %v", count, err)
	}
}
```

## TestCrashMidBatchRollsBackAndResumes

Source: `internal/sync/loop_test.go`

```go
func TestCrashMidBatchRollsBackAndResumes(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.SetState(context.Background(), store.KeyLastLedger, "9"); err != nil {
		t.Fatal(err)
	}
	writes := 0
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 12}, Apply: func(ctx context.Context, tx *store.Tx, f ingest.ContractFacts) error {
		if err := writeFact(ctx, tx, f); err != nil {
			return err
		}
		writes++
		if writes == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}}
	if err := l.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("crash: %v", err)
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "9" {
		t.Fatalf("last %q: %v", last, err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM contracts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial batch leaked %d: %v", n, err)
	}
	l.Apply = writeFact
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "12" {
		t.Fatalf("resume %q: %v", last, err)
	}
}
```

## TestRetentionGapWritesNothing

Source: `internal/sync/loop_test.go`

```go
func TestRetentionGapWritesNothing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "resume"}[existing], func(t *testing.T) {
			s := testStore(t)
			if existing {
				if err := s.SetState(context.Background(), store.KeyLastLedger, "9"); err != nil {
					t.Fatal(err)
				}
			}
			var before int
			if err := s.DB.QueryRow(`SELECT count(*) FROM sync_state`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			l := Loop{Store: s, Source: &source{oldest: 11, latest: 20}, StartLedger: 10, Apply: func(context.Context, *store.Tx, ingest.ContractFacts) error {
				t.Fatal("apply called across retention gap")
				return nil
			}}
			if err := l.Run(context.Background()); !errors.Is(err, ErrRetentionGap) {
				t.Fatal(err)
			}
			var after int
			if err := s.DB.QueryRow(`SELECT count(*) FROM sync_state`).Scan(&after); err != nil || after != before {
				t.Fatalf("state changed %d -> %d: %v", before, after, err)
			}
		})
	}
}
```
