# Stellar Wave application

Prepared 7 October 2026 for the [Stellar Wave program](https://www.drips.network/wave/stellar) on Drips. Everything below is accurate on that date.

## Project

**soroindex**: [github.com/Soroban-CII/soroindex](https://github.com/Soroban-CII/soroindex). A single Go repository.

### Description

soroindex is a public index of Soroban smart contracts that answers "which contracts are tokens?" and similar questions. SEP-47 lets a contract declare which SEPs it implements in its Wasm meta, but tools read that declaration one contract at a time, so nobody can list every contract that implements SEP-41. soroindex scans every deployed contract and records three things: what the contract declares (SEP-47 meta), what its interface actually matches (a typed check of its `contractspecv0` functions against versioned rule files), and, on testnet, whether it passed a conformance suite. It also tracks how all three change when a contract upgrades, including the CAP-85 executable references introduced in protocol 28, which upgrade many contracts at once. Our census of all 156,173 mainnet contracts on 7 October 2026 found that only 13 of 2,178 live Wasm hashes (0.60%) declare any SEP, while 120 hashes (1,141 contracts) match the SEP-41 token interface and 109 of those declare nothing. So the index leads with what code matches, tracks declarations as they grow, and helps authors add them. It is written in Go, parses Wasm without ever executing it, stores data in SQLite, and will serve an HTTP API and a CLI.

## Not already on the platform

Checked live on 2026-10-07 at 19:38 UTC, against the Drips Wave API that backs [drips.network/wave/stellar/repos](https://www.drips.network/wave/stellar/repos):

- **All 824 approved Stellar Wave repositories** were downloaded (9 pages). None has the name `soroindex`, the owner `Soroban-CII`, or the text `SEP-47`, `sep47`, `contractmeta`, `contract meta` or `interface index` in any field.
- The API's own search for `soroindex` returns **0** results. A control search for a known approved repository (`routedock`) returns 1, so search does filter.
- The closest approved project is `Miracle656/wraith`, an incoming-transfer **event** indexer for SAC/SEP-41 tokens. soroindex does not index events. It indexes what contracts declare and match. The full survey of related tools is in [Prior art](../prior-art.md).

## Links

| Item | Link | Status on 2026-10-07 |
| --- | --- | --- |
| Repository | [github.com/Soroban-CII/soroindex](https://github.com/Soroban-CII/soroindex) | Public |
| Documentation site | [soroban-cii.github.io/soroindex](https://soroban-cii.github.io/soroindex/) | Live after `scripts/repo-settings.sh` enables Pages |
| Adoption report | [report/ADOPTION.md](https://github.com/Soroban-CII/soroindex/blob/main/report/ADOPTION.md) | Mainnet census + testnet sample |
| Planned issues | [Issues labelled Stellar Wave](https://github.com/Soroban-CII/soroindex/issues?q=label%3A%22Stellar+Wave%22) | Created by `scripts/create-issues.sh` |
| CI | [Actions](https://github.com/Soroban-CII/soroindex/actions/workflows/ci.yml) | lint, test, fuzz-smoke, build |
| Go package docs | [pkg.go.dev/.../pkg/sepmeta](https://pkg.go.dev/github.com/Soroban-CII/soroindex/pkg/sepmeta) | Indexed on first request |
| Demo video | — | To be recorded by the maintainer |
| Release, binaries, Docker image | — | Not yet: tracked as issues |
| Live API | — | Not yet: the API is planned |

## Built and planned

| Built and tested | Planned |
| --- | --- |
| `pkg/sepmeta`: bounded Wasm section reader, SEP-47 meta parser (strict and lenient, every anomaly recorded), spec decoder; 4 fuzz targets ran 10 minutes each with no failure | Incremental sync loop over `getLedgers` with upgrade detection and the retention-gap guard |
| SEP-41 rule file (diffed against the live SEP-41 v0.5.2 text) and the typed interface matcher | HTTP API: `/v1/contracts`, `/v1/contracts/{id}`, `/history`, `/v1/wasm/{hash}`, `/v1/seps`, `/v1/stats`, `/v1/health` |
| Phase 0: mainnet census from Hubble and a stratified testnet sample, with `report/ADOPTION.md` | CLI: `query`, `contract`, `wasm`, `stats`, `serve` |
| SQLite store with migrations; invariants enforced in SQL and tested with raw SQL | Rule files for SEP-40, SEP-50, SEP-56 |
| Seeding an index from any contract list; stored totals verified against the census (testnet 18/18; mainnet 16/18, both differences one contract upgraded after the Hubble snapshot) | Dockerfile, release binaries, GHCR image |
| Undeclared-gap query and `gap` command; match recomputation for new rule versions | Data-lake backfill, RPC failover, adoption charts and trend |
| `LedgerSource`: reads consecutive ledgers and refuses gaps | Verified tier (soroban-guard): deferred, since under 1% of mainnet Wasm declares anything |

430 tests passed on 7 October 2026 (`go test -v ./...`: 430 PASS lines, 0 FAIL); `golangci-lint` reported 0 issues.

## Planned issues

32 issues, each with a summary, why it matters, acceptance criteria as checkboxes, tech stack and file pointers. All carry the `Stellar Wave` label, a difficulty and an area. Source: [`scripts/create-issues.sh`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/create-issues.sh).

| Area | Issues | Difficulty (trivial / medium / high) |
| --- | ---: | --- |
| Sync and ingestion | 8 | 0 / 5 / 3 |
| HTTP API | 9 | 0 / 8 / 1 |
| CLI | 3 | 1 / 2 / 0 |
| Rule files (SEP-40, SEP-50, SEP-56) | 3 | 0 / 3 / 0 |
| Parser | 2 | 2 / 0 / 0 |
| Infrastructure (Docker, releases, govulncheck, integration CI) | 4 | 1 / 3 / 0 |
| Docs and adoption tracking | 3 | 1 / 2 / 0 |

- **Sync and ingestion:** the incremental sync loop; the retention-gap stop; upgrade detection; CAP-85 reference fan-out; eviction and restoration; `--follow`; a data-lake backfill source; RPC failover.
- **HTTP API:** the server skeleton with `/v1/health`; `/v1/contracts` with tier and SEP filters; contract detail and history; Wasm detail; SEP counts and stats; rate limiting; an OpenAPI document; a 1M-row query benchmark.
- **CLI:** `query` with CSV output; `contract` and `wasm`; `stats`.
- **Rule files:** SEP-40 oracle consumer (7 functions), SEP-50 non-fungible tokens (11), SEP-56 tokenized vault (17), each reviewed against the SEP text.
- **Parser:** vectors for Unicode whitespace and edge tokens; fuzz seeds from real mainnet Wasm.
- **Infrastructure:** Dockerfile; a release workflow with binaries and image; govulncheck; a nightly testnet integration run.
- **Docs:** adoption charts; adoption tracked over time; real API examples once the API exists.

## Before submitting (maintainer)

1. Run `./scripts/repo-settings.sh` (Pages, branch protection, topics, private vulnerability reporting). Then open the docs site in a logged-out browser.
2. Run `./scripts/create-issues.sh` (preview first with `DRY_RUN=1`).
3. Record the demo video. A script of real commands is in [DEMO.md](DEMO.md).
4. Re-check that the project is still not in the approved list.
5. Install the Drips Wave GitHub App on the `Soroban-CII` organization and submit.
