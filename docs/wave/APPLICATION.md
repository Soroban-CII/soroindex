# Stellar Wave application

Prepared 9 October 2026 for the [Stellar Wave program](https://www.drips.network/wave/stellar). The live approval-list search and README comparison passed; public publication and operator checks remain pending. This package is not yet ready to submit.

## Project description

**soroindex** ([ciscokwiz/soroindex](https://github.com/ciscokwiz/soroindex)) is a public index of Soroban smart contracts by the SEPs they declare and the interfaces they match. SEP-47 metadata can be read one contract at a time; the index answers which contracts declare SEP-41, which actually match its typed interface, and how those facts change across observed upgrades, including CAP-85 executable references. The dated 7 October 2026 census covered 156,173 mainnet contracts: 13 of 2,178 live Wasm hashes (0.60%) declared any SEP, while 120 hashes (1,141 contracts) matched SEP-41 and 109 matching hashes declared nothing. Go parsers inspect Wasm without executing it, SQLite stores versioned claims, and a read-only HTTP API and CLI expose the results. Declarations and interface matches carry separate trust tiers; a verified tier is reserved for future testnet conformance work and is not a security audit.

## Built and planned

Built and tested: bounded SEP-47 metadata/spec parsing; versioned SEP-41 interface matching; Phase 0 census and report; idempotent seeding; incremental ledger sync and retention-gap detection; CAP-85 reference fan-out; archival handling; history; read-only API with pagination and rate limits; query (including bounded complete CSV export), contract, wasm, stats and serve CLI; non-root static Docker image; four-platform release candidate with licenses and checksums. The real 30-minute testnet follow check passed with final lag one ledger. The deployed-fixture upgrade check was explicitly waived by the operator; the demo does not claim that test passed.

The v0.1.0 candidate is built locally. A public release, image, Pages deployment and public API hosting are not claimed. Remaining implementation work is listed below. Stage J conformance verification remains deferred because measured declaration adoption is below the 1% gate. The Go module retains `github.com/Soroban-CII/soroindex`; repository ownership links use `ciscokwiz/soroindex` without changing imports.

## Links checklist

| Item | Intended link | Current evidence / required action |
| --- | --- | --- |
| Repository | [ciscokwiz/soroindex](https://github.com/ciscokwiz/soroindex) | Selected origin; source commits pushed to `work`. Operator reviews and merges to main. |
| Docs | [ciscokwiz.github.io/soroindex](https://ciscokwiz.github.io/soroindex/) | Strict local build passes; anonymous request currently returns 404. Operator enables Pages and checks deployment in a logged-out browser. |
| Adoption report | [report/ADOPTION.md](https://github.com/ciscokwiz/soroindex/blob/main/report/ADOPTION.md) | Recorded 7 October census, not a fresh scan. |
| Issues | [Stellar Wave issues](https://github.com/ciscokwiz/soroindex/issues?q=label%3A%22Stellar+Wave%22) | 25 validated remaining issues prepared; operator previews and creates them. |
| CI | [Actions](https://github.com/ciscokwiz/soroindex/actions/workflows/ci.yml) | Workflow defines lint, test, fuzz-smoke, build. Verify main checks after merge. |
| Release | [v0.1.0](https://github.com/ciscokwiz/soroindex/releases/tag/v0.1.0) | Candidate only; anonymous release link returns 404. Operator runs reviewed publication script. |
| Docker image | `ghcr.io/ciscokwiz/soroindex:v0.1.0` | Local image built and smoke-tested; registry publication pending. |
| Demo video | No URL yet | Operator records [the real demo](DEMO.md), supplies a public URL and checks access. |
| Live API | No hosted URL | Local read-only API exercised; hosting is optional and currently absent. |

## Live Wave eligibility and README comparison

Read-only requests to the live approved-repository page succeeded on 9 October after initial proxy denials. Its embedded response lists 824 approved repositories sorted by stars. Searches for `soroindex` and `ciscokwiz/soroindex` both returned zero results; a positive control for `routedock` returned `winsznx/routedock`. This is dated evidence, and the operator must repeat the search before submitting.

The actual GitHub-selected READMEs of the three leading approved rows—MarketPay (59 Wave-recorded stars), GreenPay (54) and OFFER-HUB (54)—were fetched and read before updating our README. See [the comparison and source hashes](README-COMPARISON.md). The direct Wave API hostname remained blocked; the public page supplied its server-rendered API responses. Environment settings include the needed public hosts, with the API hostname added for future direct checks.

## Planned issues

Generated from [`scripts/wave-issues.json`](https://github.com/ciscokwiz/soroindex/blob/work/scripts/wave-issues.json), the source consumed by `scripts/create-issues.sh`: **25 real remaining issues**. All have Summary, Why, checkbox acceptance criteria, Tech Stack and existing file pointers, plus `Stellar Wave`, difficulty and area labels. The creator checks existing open and closed issue titles before creating anything.

| Area | Count | Difficulty: trivial / medium / high |
| --- | ---: | --- |
| api | 4 | 0 / 2 / 2 |
| cli | 2 | 1 / 1 / 0 |
| docs | 4 | 1 / 3 / 0 |
| infra | 4 | 0 / 4 / 0 |
| parser | 3 | 1 / 2 / 0 |
| rules | 3 | 0 / 3 / 0 |
| sync | 5 | 0 / 2 / 3 |

### api

- docs(api): publish validated OpenAPI 3.1 specification
- feat(api): propose declared and inferred disagreement filters
- feat(api): propose historical ledger query filters
- test(api): exercise concurrent WAL readers during sync

### cli

- feat(cli): paginate undeclared gap output
- feat(cli): explain interface mismatch signatures in human output

### docs

- docs(phase0): chart declared versus inferred adoption
- feat(phase0): retain dated adoption snapshots and trends
- docs(api): add a typed Python client example
- docs(api): add a browser client with pagination and tier labels

### infra

- ci(repo): produce release SBOM and signed provenance
- ci(repo): build and smoke-test arm64 container images
- ci(repo): test the declared Go language floor
- ci(repo): propose reviewed dependency patch automation

### parser

- test(sepmeta): add Unicode whitespace boundary vectors
- test(sepmeta): seed fuzzing with real mainnet custom sections
- test(sepmeta): add nested UDT and union spec vectors

### rules

- feat(rules): add reviewed SEP-40 oracle consumer rules
- feat(rules): add reviewed SEP-50 non-fungible tokens rules
- feat(rules): add reviewed SEP-56 tokenized vaults rules

### sync

- feat(ingest): implement data-lake BackfillSource
- feat(rpc): fail over across network-verified providers
- feat(store): add a consistent online backup command
- feat(sync): expose throughput and RPC retry metrics
- feat(ingest): stream large seed CSV inputs in bounded batches

## Operator completion checklist

1. Review and merge the source branch. Recheck the dated live eligibility result before submission.
2. Preview `DRY_RUN=1 ./scripts/repo-settings.sh` and `DRY_RUN=1 ./scripts/create-issues.sh`, then run the reviewed scripts with appropriate repository rights. Settings enable Pages, private vulnerability reporting, topics and main protection.
3. Confirm docs renders and all links open in a logged-out browser. Reproduce the demo on a real index, record it and replace the missing video link.
4. Preview `./scripts/publish-release.sh`; from clean reviewed main, run `./scripts/publish-release.sh --publish` with the operator's registry login. Confirm public release assets, checksums and image access.
5. Recheck the live approved-repository list. Install the Drips Wave GitHub App for the selected repository/account and submit from the operator's account. The agent does not submit or operate that account.
