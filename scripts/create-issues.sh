#!/usr/bin/env bash
# Creates every planned issue for soroindex in one run (CLAUDE.md §7 step 41).
#
# Run by a maintainer with write access:
#   ./scripts/create-issues.sh            # Soroban-CII/soroindex
#   REPO=owner/name ./scripts/create-issues.sh
#   DRY_RUN=1 ./scripts/create-issues.sh  # print what would be created; no GitHub calls
#
# It creates the labels first, then each issue. An issue whose title already
# exists (open or closed) is skipped, so running the script twice creates no
# duplicates. Every issue is real, unsolved work as of 2026-10-07.
set -euo pipefail

REPO="${REPO:-Soroban-CII/soroindex}"
DRY_RUN="${DRY_RUN:-0}"
if [[ "$DRY_RUN" != 1 ]]; then
  command -v gh >/dev/null || { echo "gh CLI is required" >&2; exit 1; }
fi

label() {
  if [[ "$DRY_RUN" == 1 ]]; then echo "label: $1"; return; fi
  gh label create "$1" --repo "$REPO" --color "$2" --description "$3" --force >/dev/null
}
label "Stellar Wave"      "1D76DB" "Open for Stellar Wave contributors (drips.network/wave/stellar)"
label "difficulty/trivial" "C2E0C6" "Small, well-scoped; good first issue"
label "difficulty/medium"  "FBCA04" "Needs some context of the codebase"
label "difficulty/high"    "D93F0B" "Touches core design; read CLAUDE.md first"
label "area/parser"        "5319E7" "pkg/sepmeta"
label "area/sync"          "0E8A16" "internal/ingest, internal/store, sync loop"
label "area/api"           "006B75" "HTTP API"
label "area/cli"           "BFD4F2" "sep47idx commands"
label "area/rules"         "F9D0C4" "rules/*.json and the matcher"
label "area/docs"          "0075CA" "Docs site and README"
label "area/infra"         "D4C5F9" "CI, Docker, releases"

existing=""
if [[ "$DRY_RUN" != 1 ]]; then
  existing="$(gh issue list --repo "$REPO" --state all --limit 1000 --json title --jq '.[].title')"
fi
created=0 skipped=0

# issue TITLE DIFFICULTY AREA  (body on stdin)
issue() {
  local title="$1" difficulty="$2" area="$3" body
  body="$(cat)"
  if [[ -n "$existing" ]] && grep -Fxq -- "$title" <<<"$existing"; then
    echo "skip (exists): $title"; skipped=$((skipped + 1)); return
  fi
  for section in "## Summary" "## Why" "## Acceptance Criteria" "## Tech Stack" "## Pointers"; do
    grep -Fq -- "$section" <<<"$body" || { echo "issue '$title' lacks '$section'" >&2; exit 1; }
  done
  if [[ "$DRY_RUN" == 1 ]]; then
    echo "would create [$difficulty/$area]: $title"; created=$((created + 1)); return
  fi
  gh issue create --repo "$REPO" --title "$title" \
    --label "Stellar Wave" --label "difficulty/$difficulty" --label "area/$area" \
    --body "$body" >/dev/null
  echo "created: $title"; created=$((created + 1))
}

# ---------------------------------------------------------------- sync

issue "feat(sync): incremental sync loop over getLedgers" high sync <<'EOF'
## Summary
Implement `sep47idx sync` without `--seed`: read ledgers from `last_ledger + 1` with `ingest.LedgerSource`, apply each batch of up to 200 ledgers, and advance `last_ledger`, all in one transaction per batch.

## Why
Today an index can only be seeded. Without the loop it goes stale the moment it is built (CLAUDE.md §5.9 steps 1-4).

## Acceptance Criteria
- [ ] On first run, `sync` requires `--seed` or `--start-ledger` and exits 1 with a message otherwise.
- [ ] Each batch's changes and the new `last_ledger` commit in one transaction.
- [ ] Replaying the same ledgers twice produces identical database contents (test compares a full table dump).
- [ ] Cancelling the context after N writes leaves `last_ledger` at the previous batch; a rerun completes it (test).
- [ ] New contracts, new code (fetched once) and instance updates are written through `ingest.Indexer`.

## Tech Stack
Go 1.27, `modernc.org/sqlite`, go-stellar-sdk `xdr`.

## Pointers
`internal/ingest/ledgers.go` (LedgerSource, never skips), `internal/ingest/changes.go` (ExtractContractFacts), `internal/ingest/indexer.go`, `internal/store/write.go`, `cmd/sep47idx/cmd_sync.go`.
EOF

issue "feat(sync): stop with ErrRetentionGap instead of skipping ledgers" medium sync <<'EOF'
## Summary
Before reading, compare `last_ledger + 1` with the RPC's oldest ledger (`LedgerSource.Window`). If it is older, exit with `ErrRetentionGap` and a message telling the operator to seed or backfill. Write nothing.

## Why
RPC nodes keep about 7 days of ledgers (testnet: 120,959 ledgers on 2026-10-07). An index that falls further behind must say so, never skip silently (CLAUDE.md §5.9 step 2).

## Acceptance Criteria
- [ ] `ErrRetentionGap` sentinel in the sync package; the CLI exits non-zero.
- [ ] A test shows the database is byte-identical before and after the failed run.
- [ ] The window is read from a successful getLedgers call, never by parsing error text.

## Tech Stack
Go.

## Pointers
`internal/ingest/ledgers.go` (`Window`), `internal/store`.
EOF

issue "feat(sync): detect upgrades from instance updates" medium sync <<'EOF'
## Summary
When an instance entry is updated with a different resolved Wasm hash, close the current `contract_versions` row at that ledger and open a new one.

## Why
Upgrades change what a contract declares and matches; the history is one of the three things the index exists to track.

## Acceptance Criteria
- [ ] An instance update with a new hash creates exactly one new version row and closes the old one (test).
- [ ] An update with the same hash (instance storage written) creates no version row.
- [ ] A switch between a direct hash and a CAP-85 reference is recorded.
- [ ] Verifications for the old hash become stale (when the verified tier exists).

## Tech Stack
Go, SQLite.

## Pointers
`store.Tx.SetVersion` already implements the row logic and is tested (`TestSetVersion`); wire it to `ContractFacts.Instances`.
EOF

issue "feat(sync): fan out CAP-85 executable-reference upgrades" high sync <<'EOF'
## Summary
When an `ScvExecutableTag` entry changes its Wasm hash, upgrade every contract that references `(owner, tag)` at that ledger, in the same transaction.

## Why
CAP-85 (protocol 28) lets one entry upgrade many contracts without touching their instance entries. Watching instances alone would miss those upgrades and show stale claims as current, which overstates trust.

## Acceptance Criteria
- [ ] A reference update upgrades every referencing contract, and only those (test).
- [ ] A reference update and an instance update in the same ledger resolve in apply order.
- [ ] A contract whose reference is missing or archived has `current_wasm_hash = NULL` and reports no current claims (test).

## Tech Stack
Go, go-stellar-sdk `xdr` (`ContractExecutableExternalRef`, `ScvExecutableTag`).

## Pointers
`internal/ingest/instance.go` (`DecodeExecRef`), `ContractFacts.ExecRefs`, `exec_refs` table, `idx_contracts_ref`.
EOF

issue "feat(sync): handle eviction and restoration of code and instances" medium sync <<'EOF'
## Summary
Use `ContractFacts.Evicted` to mark code (`parse_status='archived'`) and instances (`contracts.archived=1`); when an entry is restored, clear the flag and fetch code that was never parsed.

## Why
On 2026-10-07, 3,083 of 5,261 mainnet Wasm hashes and 74,959 of 156,173 instances were archived. The index must keep the last known data and never delete claims on archival.

## Acceptance Criteria
- [ ] An evicted code entry keeps its claims (test).
- [ ] A restored instance is no longer flagged archived.
- [ ] Restored code that was archived before it was ever parsed is fetched and analyzed once.

## Tech Stack
Go.

## Pointers
`internal/ingest/changes.go` (`Restored`, `Evicted`), `store.Tx.UpsertWasm` (archived never overwrites parsed data), `store.Tx.HasWasm`.
EOF

issue "feat(sync): --follow mode with polling interval" medium sync <<'EOF'
## Summary
`sync --follow` keeps running: when caught up it re-checks `getLatestLedger`, reports `caught_up`, and polls every `--interval` (default 5s).

## Why
A public index must stay current without a cron job restarting it.

## Acceptance Criteria
- [ ] Lag (`latest - last_ledger`) is logged each cycle.
- [ ] SIGINT finishes or rolls back the current batch cleanly.
- [ ] Transient RPC errors back off and retry; the loop does not exit on one failed call.

## Tech Stack
Go, `log/slog`.

## Pointers
`cmd/sep47idx/cmd_sync.go`, `internal/rpc` (retries and backoff already built).
EOF

issue "test(sync): nightly testnet integration run" medium infra <<'EOF'
## Summary
An integration test (build tag `integration`) follows testnet for 30 minutes from the tip, and a workflow `integration.yml` runs it nightly and on demand.

## Why
Unit tests use recorded ledgers. Only a live run shows the loop keeps up with real traffic (CLAUDE.md §7 step 27).

## Acceptance Criteria
- [ ] No errors over 30 minutes; lag under 10 ledgers at the end.
- [ ] Workflow triggers: `schedule` (nightly) and `workflow_dispatch`.
- [ ] Not a required check for pull requests.

## Tech Stack
Go, GitHub Actions.

## Pointers
`.github/workflows/ci.yml` for the action pins and Go setup.
EOF

# ---------------------------------------------------------------- api

issue "feat(api): serve command and /v1/health" medium api <<'EOF'
## Summary
`sep47idx serve --addr :8080` serves `/v1` with the standard library router, opens the database read-only, and implements `GET /v1/health` (`{status, network, last_ledger, latest_ledger, lag}`).

## Why
Every other endpoint builds on this skeleton: error shape, CORS, read-only store, timeouts.

## Acceptance Criteria
- [ ] Errors always use `{"error":{"code":"not_found|bad_request|rate_limited|internal","message":"..."}}` with the matching status.
- [ ] CORS enabled for GET.
- [ ] The store opens with `ReadOnly: true` (`mode=ro`); a test shows a write fails.
- [ ] Handler tests for health, 404 and bad input.

## Tech Stack
Go `net/http` (1.22+ routing patterns), no framework.

## Pointers
`internal/store/store.go` (`Options.ReadOnly`, `ErrSchemaTooOld`), `docs/api.md` (the design).
EOF

issue "feat(api): GET /v1/contracts with tier and SEP filters" high api <<'EOF'
## Summary
List contracts satisfying `implements` (comma list, AND), `implements_any` (OR), `tier` (`declared` default, `inferred`, `verified`, `protocol`) and `kind`, with keyset pagination (`cursor`, `limit` default 50, max 500). `/contracts` is an alias.

## Why
This is the question the project exists to answer: "list every contract that implements SEP-41".

## Acceptance Criteria
- [ ] Tiers are never mixed: a test asserts a SAC never appears under `tier=declared`, and appears under `tier=protocol` for SEP-41.
- [ ] Tests for pagination, AND vs OR, each tier, bad input and an unknown SEP.
- [ ] Contract IDs validated as `C...` strkeys before any query.

## Tech Stack
Go, SQLite.

## Pointers
`claims` view and indexes in `migrations/0001_init.sql`; `store.Gap` for the inferred query shape.
EOF

issue "feat(api): GET /v1/contracts/{id} detail" medium api <<'EOF'
## Summary
Return one contract with claims grouped by tier, its version history, `exec_ref`, and archived flag, in the fixed shape of CLAUDE.md §5.11.

## Why
Integrators need one call to see what a contract says, matches and has been.

## Acceptance Criteria
- [ ] Shape matches §5.11 exactly, including `parser_version` and `as_of_ledger`.
- [ ] `verified` is `null` when not run.
- [ ] 404 for an unknown contract; 400 for a malformed ID.

## Tech Stack
Go.

## Pointers
`CLAUDE.md` §5.11, `contract_versions`, `interface_matches`.
EOF

issue "feat(api): GET /v1/contracts/{id}/history" medium api <<'EOF'
## Summary
Every Wasm version of a contract with its ledger range and the claims and matches at each.

## Why
Shows how what a contract declares changed across upgrades.

## Acceptance Criteria
- [ ] Ordered by `from_ledger`; the current version has `to_ledger: null`.
- [ ] CAP-85 versions carry their `exec_ref`.
- [ ] Tests for a contract with 0, 1 and several upgrades.

## Tech Stack
Go.

## Pointers
`contract_versions`, `store.Tx.SetVersion`.
EOF

issue "feat(api): GET /v1/wasm/{hash}" medium api <<'EOF'
## Summary
Parsed meta, claims, anomalies, interface matches and the contracts currently using one Wasm hash.

## Why
Factory deployments share one Wasm across thousands of contracts; the Wasm is the natural unit for authors to check.

## Acceptance Criteria
- [ ] Hash validated as 64 lowercase hex characters.
- [ ] Includes `parse_status` and `parse_error` for archived or malformed code.
- [ ] Contracts list is paginated.

## Tech Stack
Go.

## Pointers
`wasm.meta_json`, `wasm.functions_json`, `parse_anomalies`.
EOF

issue "feat(api): GET /v1/seps and GET /v1/stats" medium api <<'EOF'
## Summary
`/v1/seps`: counts per SEP by tier. `/v1/stats`: adoption numbers, the undeclared gap, anomalies, last sync.

## Why
The adoption numbers in report/ADOPTION.md should be available live, not only as a document.

## Acceptance Criteria
- [ ] Numbers equal `store.Totals` for the same database (test).
- [ ] Every number states the ledger it is as of.

## Tech Stack
Go.

## Pointers
`internal/store/totals.go`.
EOF

issue "feat(api): per-IP rate limiting" medium api <<'EOF'
## Summary
`serve --rate-limit N` limits each client IP to N requests per minute; 0 disables it. Over the limit returns 429 with the standard error shape.

## Why
A public endpoint needs a basic guard against one client exhausting it.

## Acceptance Criteria
- [ ] Token bucket per IP with bounded memory (old entries expire).
- [ ] Test: N requests succeed, the next is 429, and service resumes after the window.

## Tech Stack
Go standard library only.

## Pointers
`docs/api.md`.
EOF

issue "docs(api): OpenAPI 3.1 document for /v1" medium api <<'EOF'
## Summary
Write `docs/openapi.yaml` describing every `/v1` endpoint, parameter and response shape, and test that real responses validate against it.

## Why
Integrators generate clients from OpenAPI; reviewers read it to see the contract.

## Acceptance Criteria
- [ ] Covers all endpoints, the error shape and pagination.
- [ ] A test validates recorded handler responses against the document.
- [ ] Linked from `docs/api.md`.

## Tech Stack
OpenAPI 3.1, Go tests.

## Pointers
`CLAUDE.md` §5.11.
EOF

issue "perf(store): benchmark implements=41 on 1M claim rows" medium api <<'EOF'
## Summary
Build a synthetic database with 1,000,000 claim rows and record p50/p95 for the `implements=41` query in `docs/`. Materialize the `claims` view only if p95 exceeds 200 ms, and show before and after numbers.

## Why
The view joins versions and claims; whether it needs materializing must be measured, not guessed (CLAUDE.md §7 step 31).

## Acceptance Criteria
- [ ] Benchmark is reproducible from one command.
- [ ] Numbers recorded with the machine, date and Go version.

## Tech Stack
Go benchmarks, SQLite.

## Pointers
`migrations/0001_init.sql` (`claims` view, `idx_claims_sep`).
EOF

# ---------------------------------------------------------------- cli

issue "feat(cli): query command with --json and --csv" medium cli <<'EOF'
## Summary
`sep47idx query --implements 41 [--implements-any ...] [--tier declared] [--kind wasm] [--json] [--csv]` lists matching contracts from the local database.

## Why
The same question as `/v1/contracts`, without running a server.

## Acceptance Criteria
- [ ] Same filters and tier rules as the API (share the query code).
- [ ] CSV output has a header row and is streamable for large results.
- [ ] Exit code 2 when nothing matches.

## Tech Stack
Go `flag`, `encoding/csv`.

## Pointers
`cmd/sep47idx/cmd_gap.go` for the command pattern.
EOF

issue "feat(cli): contract and wasm commands" medium cli <<'EOF'
## Summary
`sep47idx contract <C...> [--history] [--json]` and `sep47idx wasm <hash> [--json]`.

## Why
Contract authors need to check their own declaration and interface verdict (docs/contract-authors.md promises this).

## Acceptance Criteria
- [ ] `wasm` shows each `sep` token with its anomaly and the SEP-41 verdict with missing and mismatched functions.
- [ ] Exit code 2 for an unknown contract or hash.

## Tech Stack
Go.

## Pointers
`internal/store`, `docs/contract-authors.md`.
EOF

issue "feat(cli): stats command" trivial cli <<'EOF'
## Summary
`sep47idx stats [--json]` prints adoption numbers and sync lag from the local database.

## Why
Operators need a quick health and adoption check without the API.

## Acceptance Criteria
- [ ] Same numbers as `store.Totals`.
- [ ] Shows `last_ledger`, and lag when an RPC URL is configured.

## Tech Stack
Go.

## Pointers
`internal/store/totals.go`, `cmd/sep47idx/cmd_gap.go`.
EOF

# ---------------------------------------------------------------- rules

issue "feat(rules): rule file for SEP-40 (oracle consumer)" medium rules <<'EOF'
## Summary
Add `rules/sep-0040.json` for SEP-40 (Oracle Consumer Interface, v0.1.0): `base`, `assets`, `decimals`, `resolution`, `price`, `prices`, `lastprice`.

## Why
Two mainnet Wasm hashes declare SEP-40 (2026-10-07); the index can say nothing about whether their interface matches.

## Acceptance Criteria
- [ ] Each function and type reviewed against the current SEP-40 text; the commit read is cited in `source`.
- [ ] User-defined types (`Asset`, `PriceData`) expressed as `udt:<Name>`, and the PR explains how they are matched.
- [ ] `make rules-check` passes; matcher tests cover one match and one mismatch.

## Tech Stack
JSON, Go tests.

## Pointers
`rules/sep-0041.json`, `docs/rule-files.md` (PR checklist), stellar-protocol `ecosystem/sep-0040.md`.
EOF

issue "feat(rules): rule file for SEP-50 (non-fungible tokens)" medium rules <<'EOF'
## Summary
Add `rules/sep-0050.json` for SEP-50 (Non-Fungible Tokens, v0.1.0): 11 functions including `owner_of`, `transfer`, `transfer_from`, `approve`, `approve_for_all`, `token_uri`.

## Why
NFT contracts are a common question ("which contracts are NFTs?") and have no inferred tier today.

## Acceptance Criteria
- [ ] Reviewed against the current SEP-50 text, commit cited in `source`.
- [ ] Optional functions, if any, in `optional`, not `required`.
- [ ] `make rules-check` passes; matcher tests cover one match and one mismatch.

## Tech Stack
JSON, Go tests.

## Pointers
`docs/rule-files.md`, stellar-protocol `ecosystem/sep-0050.md`.
EOF

issue "feat(rules): rule file for SEP-56 (tokenized vault)" medium rules <<'EOF'
## Summary
Add `rules/sep-0056.json` for SEP-56 (Tokenized Vault Standard, v0.1.2): 17 functions including `deposit`, `mint`, `withdraw`, `redeem` and their `preview_*`/`max_*` pairs.

## Why
Vaults hold user funds; knowing which contracts expose the vault interface is useful to integrators and auditors.

## Acceptance Criteria
- [ ] Reviewed against the current SEP-56 text, commit cited in `source`.
- [ ] The PR states whether a SEP-56 vault must also satisfy SEP-41 and how the index reports that.
- [ ] `make rules-check` passes; matcher tests cover one match and one mismatch.

## Tech Stack
JSON, Go tests.

## Pointers
`docs/rule-files.md`, stellar-protocol `ecosystem/sep-0056.md`.
EOF

# ---------------------------------------------------------------- parser

issue "test(sepmeta): parser vectors for Unicode whitespace and edge tokens" trivial parser <<'EOF'
## Summary
Add vectors under `testdata/vectors/` for: non-breaking space and other Unicode whitespace around a token, a value of exactly 4,096 bytes, 50 separate `sep` entries, and `+41`.

## Why
The parser trims ASCII whitespace only, by design. Vectors pin that behavior so a change cannot slip in unnoticed.

## Acceptance Criteria
- [ ] Each vector has lenient and strict golden JSON (`go test ./pkg/sepmeta -run TestVectors -update`, then review the diff).
- [ ] `TestParseSEP47Rules` gains a hand-written case for each.

## Tech Stack
Go tests.

## Pointers
`testdata/vectors/README.md`, `pkg/sepmeta/sep47.go`.
EOF

issue "test(sepmeta): seed fuzz corpora with real mainnet Wasm sections" trivial parser <<'EOF'
## Summary
Add the meta and spec sections of real mainnet Wasm (the 13 declaring hashes in `report/mainnet-raw.csv` and a sample of others) to the fuzz seed corpora.

## Why
Seeds today are the golden fixtures, built by one toolchain. Real-world sections from other toolchains and SDK versions give the fuzzer better starting points.

## Acceptance Criteria
- [ ] A script fetches the sections by hash and writes them as seeds; the script is committed.
- [ ] Seeds are small (sections only, not whole modules).
- [ ] `make fuzz-smoke` still passes.

## Tech Stack
Go fuzzing.

## Pointers
`pkg/sepmeta/fuzz_test.go`, `internal/ingest/fetch.go`.
EOF

# ---------------------------------------------------------------- infra

issue "build(repo): Dockerfile (multi-stage, distroless, non-root)" medium infra <<'EOF'
## Summary
A Dockerfile that builds `sep47idx` with `CGO_ENABLED=0` and runs it on a distroless base as a non-root user.

## Why
Self-hosting today means building from source (docs/self-hosting.md).

## Acceptance Criteria
- [ ] Image under 30 MB; runs as a non-root UID.
- [ ] `docker run ... sep47idx version` works.
- [ ] The database lives on a mounted volume, never in the image.

## Tech Stack
Docker.

## Pointers
`Makefile` (`build` target), `docs/self-hosting.md`.
EOF

issue "ci(repo): release workflow with binaries and image" medium infra <<'EOF'
## Summary
On a version tag, build binaries for linux/darwin x amd64/arm64 and push a Docker image to GHCR; attach the binaries and checksums to the GitHub release.

## Why
Users should not need Go to run the index.

## Acceptance Criteria
- [ ] Reproducible: `-trimpath`, version set via `-X main.version`.
- [ ] SHA-256 checksums file attached.
- [ ] Actions pinned to commit SHAs, as in ci.yml.

## Tech Stack
GitHub Actions.

## Pointers
`.github/workflows/ci.yml` (build matrix).
EOF

issue "ci(repo): run govulncheck in CI" trivial infra <<'EOF'
## Summary
Add a `govulncheck ./...` step to the lint job, with the tool version pinned.

## Why
The index parses hostile input; known vulnerabilities in dependencies should fail CI.

## Acceptance Criteria
- [ ] Pinned version, built with the CI Go toolchain.
- [ ] Passes on main today, or the PR fixes what it finds.

## Tech Stack
GitHub Actions, govulncheck.

## Pointers
`.github/workflows/ci.yml` (lint job).
EOF

issue "feat(ingest): data-lake BackfillSource" high sync <<'EOF'
## Summary
Implement a `BackfillSource` that reads historical `LedgerCloseMeta` from a public ledger data lake, so an index can be rebuilt from history instead of a seed list.

## Why
RPC retention is about 7 days. A seed gives current state but not upgrade history before the seed.

## Acceptance Criteria
- [ ] Reads a ledger range and yields the same `ContractFacts` as `LedgerSource`.
- [ ] Reprocessing a range is idempotent.
- [ ] Any new dependency is justified in the PR and agreed by a maintainer first.

## Tech Stack
Go, XDR.

## Pointers
`internal/ingest/seed.go` (`BackfillSource`), `internal/ingest/ledgers.go`.
EOF

issue "feat(rpc): failover across several RPC URLs" medium sync <<'EOF'
## Summary
Accept several `--rpc-url` values and move to the next when one fails repeatedly. Every endpoint's passphrase must match the configured network.

## Why
On 2026-10-07, five public mainnet endpoints answered identically; one public endpoint returned 404. A single endpoint is a single point of failure.

## Acceptance Criteria
- [ ] Passphrase checked for every endpoint at startup.
- [ ] Failover logged with the endpoint host only, never full responses.
- [ ] Test with two fake servers, the first failing.

## Tech Stack
Go.

## Pointers
`internal/rpc/client.go` (retries), `internal/config`.
EOF

# ---------------------------------------------------------------- docs

issue "docs(phase0): charts on the adoption page" medium docs <<'EOF'
## Summary
Generate SVG charts from `report/*-summary.json` (declare rate by hash and by contract, the SEP-41 OK-count histogram) and show them on the adoption page.

## Why
The adoption numbers are the project's headline; a chart reads faster than a table.

## Acceptance Criteria
- [ ] Charts generated from the summary JSON by a committed script, never drawn by hand.
- [ ] Every chart states its date and ledger.
- [ ] Accessible: a text table stays alongside each chart.

## Tech Stack
Go or Python script, SVG, MkDocs.

## Pointers
`internal/phase0/markdown.go`, `docs/adoption.md`.
EOF

issue "feat(phase0): track adoption over time" medium docs <<'EOF'
## Summary
Keep a dated history of Phase 0 headline numbers (`report/history.csv`) and render a trend table in ADOPTION.md.

## Why
"Interface index + adoption tracker" is the headline: whether declarations grow is the question to track monthly.

## Acceptance Criteria
- [ ] Each run appends one row per network: date, ledger, declare rates, SEP-41 match, gap.
- [ ] Re-running on the same day replaces that day's row.

## Tech Stack
Go.

## Pointers
`internal/phase0/run.go` (`WriteSummary`), `internal/phase0/markdown.go`.
EOF

issue "docs(api): capture real request and response examples" trivial docs <<'EOF'
## Summary
Once the `/v1` endpoints exist, replace the design table in `docs/api.md` with real requests and responses captured from a running server.

## Why
The docs rule is that every example comes from a real run.

## Acceptance Criteria
- [ ] One `curl` and its real response per endpoint, with the date.
- [ ] The "Not built yet" warning removed.

## Tech Stack
MkDocs.

## Pointers
`docs/api.md`. Depends on the API issues.
EOF

echo "done: $created created, $skipped skipped"
