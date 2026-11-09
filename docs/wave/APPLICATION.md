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
| Issues | [Stellar Wave issues](https://github.com/Soroban-CII/soroindex/issues?q=label%3A%22Stellar+Wave%22) | All 25 planned issues are open (#15, #17, #20–#24, #28–#31, #39–#52). 21 issues were closed between 8 and 9 November 2026, each with evidence. |
| Docker image | `ghcr.io/soroban-cii/soroindex:v0.1.0` | Not yet published. Actions → release → Run workflow with `v0.1.0`, then make the package public. |
| Demo video | none yet | The operator records [the demo](DEMO.md). |
| Live API | none | Hosting is optional and absent. |

## Planned issues

Generated from [`scripts/wave-issues.json`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/wave-issues.json), the list `scripts/create-issues.sh` reads. All 25 remaining issues are open on GitHub, and each entry records its issue number so the creator never duplicates one. Each has Summary, Why, checkbox Acceptance Criteria, Tech Stack and Pointers to existing files, plus `Stellar Wave`, difficulty and area labels.

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

- [#15](https://github.com/Soroban-CII/soroindex/issues/15) docs(api): publish validated OpenAPI 3.1 specification
- [#40](https://github.com/Soroban-CII/soroindex/issues/40) feat(api): propose declared and inferred disagreement filters
- [#41](https://github.com/Soroban-CII/soroindex/issues/41) feat(api): propose historical ledger query filters
- [#42](https://github.com/Soroban-CII/soroindex/issues/42) test(api): exercise concurrent WAL readers during sync

### cli

- [#43](https://github.com/Soroban-CII/soroindex/issues/43) feat(cli): show interface evidence in contract output
- [#17](https://github.com/Soroban-CII/soroindex/issues/17) feat(cli): decide and implement query exit codes for empty results

### docs

- [#30](https://github.com/Soroban-CII/soroindex/issues/30) docs(phase0): chart declared versus inferred adoption
- [#31](https://github.com/Soroban-CII/soroindex/issues/31) feat(phase0): retain dated adoption snapshots and trends
- [#47](https://github.com/Soroban-CII/soroindex/issues/47) docs(api): add a typed Python client example
- [#48](https://github.com/Soroban-CII/soroindex/issues/48) docs(api): add a browser client with pagination and tier labels

### infra

- [#49](https://github.com/Soroban-CII/soroindex/issues/49) ci(repo): produce release SBOM and signed provenance
- [#50](https://github.com/Soroban-CII/soroindex/issues/50) ci(repo): build and smoke-test arm64 container images
- [#51](https://github.com/Soroban-CII/soroindex/issues/51) ci(repo): test the declared Go language floor
- [#52](https://github.com/Soroban-CII/soroindex/issues/52) ci(repo): propose reviewed dependency patch automation

### parser

- [#23](https://github.com/Soroban-CII/soroindex/issues/23) test(sepmeta): add Unicode whitespace boundary vectors
- [#24](https://github.com/Soroban-CII/soroindex/issues/24) test(sepmeta): seed fuzzing with real mainnet custom sections
- [#39](https://github.com/Soroban-CII/soroindex/issues/39) test(sepmeta): add nested UDT and union spec vectors

### rules

- [#20](https://github.com/Soroban-CII/soroindex/issues/20) feat(rules): add reviewed SEP-40 oracle consumer rules
- [#21](https://github.com/Soroban-CII/soroindex/issues/21) feat(rules): add reviewed SEP-50 non-fungible tokens rules
- [#22](https://github.com/Soroban-CII/soroindex/issues/22) feat(rules): add reviewed SEP-56 tokenized vaults rules

### sync

- [#28](https://github.com/Soroban-CII/soroindex/issues/28) feat(ingest): implement data-lake BackfillSource
- [#29](https://github.com/Soroban-CII/soroindex/issues/29) feat(rpc): fail over across network-verified providers
- [#44](https://github.com/Soroban-CII/soroindex/issues/44) feat(store): add a consistent online backup command
- [#45](https://github.com/Soroban-CII/soroindex/issues/45) feat(sync): expose throughput and RPC retry metrics
- [#46](https://github.com/Soroban-CII/soroindex/issues/46) feat(ingest): stream large seed CSV inputs in bounded batches

## Operator checklist

Done on 9 November 2026:
- The 14 new issues were created (#39–#52).
- `main` is protected.
- Topics and the website are set.

Remaining:
1. Confirm private vulnerability reporting is enabled: https://github.com/Soroban-CII/soroindex/security/advisories/new should show a report form.
2. Publish the image: Actions → release → Run workflow with `v0.1.0`, then set the GHCR package to public.
3. Record the demo from [DEMO.md](DEMO.md) and add its public URL above.
4. Recheck the approved-repository list, then submit from the operator's account. The agent does not submit or operate that account.
