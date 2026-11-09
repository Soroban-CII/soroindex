# Stellar Wave application

Updated 9 November 2026 for the [Stellar Wave program](https://www.drips.network/wave/stellar). This replaces the 9 October draft, which predated the public release.

## Project description

**soroindex** ([Soroban-CII/soroindex](https://github.com/Soroban-CII/soroindex)) is a public index of Soroban smart contracts. It records the SEPs each contract declares, the interfaces it matches, and how both change across upgrades. SEP-47 metadata can be read one contract at a time. The index answers network-wide questions: which contracts declare SEP-41, which actually match its typed interface, and how those facts change across observed upgrades, including CAP-85 executable references.

The dated 7 October 2026 census covered 156,173 mainnet contracts. 13 of 2,178 live Wasm hashes (0.60%) declared any SEP. 120 hashes (1,141 contracts) matched SEP-41, and 109 matching hashes declared nothing.

Go parsers inspect Wasm without executing it, SQLite stores versioned claims, and a read-only HTTP API and CLI expose the results. Declarations and interface matches carry separate trust tiers. A verified tier is reserved for future testnet conformance work and is not a security audit.

## Built and planned

**Built and tested on `main`:**
- bounded SEP-47 metadata and spec parsing (four fuzz targets);
- versioned SEP-41 interface matching;
- the Phase 0 census and report;
- idempotent seeding;
- incremental ledger sync, including Protocol 30 ledgers, with retention-gap detection;
- CAP-85 reference fan-out, archival handling and history;
- a read-only API with pagination and rate limits;
- the CLI: `query` (including CSV export), `gap`, `contract`, `wasm` (naming missing and mismatched functions), `stats` (with an optional `--lag` sync check), `serve` and `sync`;
- a non-root static Docker image, measured at 27,772,533 bytes;
- a tag-triggered release workflow that drafts releases and pushes a GHCR image.

**Published:**
- the [v0.1.0 release](https://github.com/Soroban-CII/soroindex/releases/tag/v0.1.0) on 9 November 2026: four static archives with `SHA256SUMS` and `BUILD.json`, built from `3abe5a3`;
- the [documentation site](https://soroban-cii.github.io/soroindex/), on GitHub Pages.

**Not claimed:**
- No container image is published yet. The release workflow can push one on request.
- No hosted API.
- No demo video yet.
- The verified tier is deferred, because measured declaration adoption is below the 1% gate in CLAUDE.md.
- The deployed-fixture upgrade check was waived by the operator. Offline tests cover upgrades.

## Links checklist

| Item | Link | Status on 9 November 2026 |
| --- | --- | --- |
| Repository | [Soroban-CII/soroindex](https://github.com/Soroban-CII/soroindex) | Public. CI (`lint`, `test`, `fuzz-smoke`, `build`), security and docs workflows green on `main`. |
| Docs | [soroban-cii.github.io/soroindex](https://soroban-cii.github.io/soroindex/) | Deployed by the `docs` workflow. |
| Release | [v0.1.0](https://github.com/Soroban-CII/soroindex/releases/tag/v0.1.0) | Published. Asset digests match the local build. |
| Adoption report | [report/ADOPTION.md](https://github.com/Soroban-CII/soroindex/blob/main/report/ADOPTION.md) | Census of 7 October 2026. |
| Issues | [Stellar Wave issues](https://github.com/Soroban-CII/soroindex/issues?q=label%3A%22Stellar+Wave%22) | 11 of the 25 planned issues are open. `scripts/create-issues.sh` opens the rest. |
| Docker image | `ghcr.io/soroban-cii/soroindex:v0.1.0` | Not yet published. Actions → release → Run workflow with `v0.1.0`, then make the package public. |
| Demo video | none yet | The operator records [the demo](DEMO.md). |
| Live API | none | Hosting is optional and absent. |

## Planned issues

Generated from [`scripts/wave-issues.json`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/wave-issues.json), the list `scripts/create-issues.sh` reads. There are 25 remaining issues; 11 are already open on GitHub and are linked, not duplicated. Each has Summary, Why, checkbox Acceptance Criteria, Tech Stack and Pointers to existing files, plus `Stellar Wave`, difficulty and area labels.

| Area | Count | Difficulty: trivial / medium / high |
| --- | ---: | --- |
| api | 4 | 0 / 2 / 2 |
| cli | 2 | 2 / 0 / 0 |
| docs | 4 | 1 / 3 / 0 |
| infra | 4 | 0 / 4 / 0 |
| parser | 3 | 1 / 2 / 0 |
| rules | 3 | 0 / 3 / 0 |
| sync | 5 | 0 / 2 / 3 |

### api

- docs(api): publish validated OpenAPI 3.1 specification (open as Soroban-CII/soroindex#15)
- feat(api): propose declared and inferred disagreement filters
- feat(api): propose historical ledger query filters
- test(api): exercise concurrent WAL readers during sync

### cli

- feat(cli): show interface evidence in contract output
- feat(cli): decide and implement query exit codes for empty results (open as Soroban-CII/soroindex#17)

### docs

- docs(phase0): chart declared versus inferred adoption (open as Soroban-CII/soroindex#30)
- feat(phase0): retain dated adoption snapshots and trends (open as Soroban-CII/soroindex#31)
- docs(api): add a typed Python client example
- docs(api): add a browser client with pagination and tier labels

### infra

- ci(repo): produce release SBOM and signed provenance
- ci(repo): build and smoke-test arm64 container images
- ci(repo): test the declared Go language floor
- ci(repo): propose reviewed dependency patch automation

### parser

- test(sepmeta): add Unicode whitespace boundary vectors (open as Soroban-CII/soroindex#23)
- test(sepmeta): seed fuzzing with real mainnet custom sections (open as Soroban-CII/soroindex#24)
- test(sepmeta): add nested UDT and union spec vectors

### rules

- feat(rules): add reviewed SEP-40 oracle consumer rules (open as Soroban-CII/soroindex#20)
- feat(rules): add reviewed SEP-50 non-fungible tokens rules (open as Soroban-CII/soroindex#21)
- feat(rules): add reviewed SEP-56 tokenized vaults rules (open as Soroban-CII/soroindex#22)

### sync

- feat(ingest): implement data-lake BackfillSource (open as Soroban-CII/soroindex#28)
- feat(rpc): fail over across network-verified providers (open as Soroban-CII/soroindex#29)
- feat(store): add a consistent online backup command
- feat(sync): expose throughput and RPC retry metrics
- feat(ingest): stream large seed CSV inputs in bounded batches

## Operator checklist

1. Run `DRY_RUN=1 ./scripts/create-issues.sh`, then `./scripts/create-issues.sh` with repository rights.
2. Publish the image: Actions → release → Run workflow with `v0.1.0`, then set the GHCR package to public.
3. Settings: enable private vulnerability reporting; protect `main` with required checks `lint`, `test`, `fuzz-smoke` and `build`; add the repository topics (`scripts/repo-settings.sh` does all three).
4. Record the demo from [DEMO.md](DEMO.md) and add its public URL above.
5. Recheck the approved-repository list, then submit from the operator's account. The agent does not submit or operate that account.
