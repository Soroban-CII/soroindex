# soroindex

A public index of Soroban smart contracts by the SEPs they declare, the interfaces they match, and how both change on upgrade.

[![CI](https://github.com/Soroban-CII/soroindex/actions/workflows/ci.yml/badge.svg)](https://github.com/Soroban-CII/soroindex/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Soroban-CII/soroindex/pkg/sepmeta.svg)](https://pkg.go.dev/github.com/Soroban-CII/soroindex/pkg/sepmeta)
[![Docs](https://img.shields.io/badge/docs-soroban--cii.github.io%2Fsoroindex-informational)](https://soroban-cii.github.io/soroindex/)

A public index of Soroban smart contracts that answers "which contracts are tokens?" and similar questions. For every deployed contract it records what the contract says it implements, what its code interface actually matches, and, on testnet, what it was shown to do when tested. It tracks how those answers change when a contract is upgraded. Anyone can query it through an HTTP API or a command-line tool.

> **Status, 8 October 2026:** pre-release. The parser, adoption census, seeding, gap queries, match recomputation and incremental sync are built and tested offline. Incremental sync supports crash resume, retention-gap detection, upgrades and CAP-85 reference updates. Its live testnet follow and deployment-upgrade checks are pending. The HTTP API and Docker image are planned. See [What is built](#what-is-built-and-what-is-planned).

## Questions it answers

1. Which contracts declare that they implement SEP-41 (or any SEP)?
2. Which contracts actually expose the SEP-41 token interface, whether or not they declare it?
3. When a contract upgrades, did what it declares or matches change?

## Adoption today

From the Phase 0 census of every mainnet contract (ledger 64,817,831, 7 October 2026; full method in [report/ADOPTION.md](report/ADOPTION.md)):

| | By unique Wasm hash | By contract |
| --- | ---: | ---: |
| Declares any SEP in `sep` meta (SEP-47) | 13 of 2,178 (0.60%) | 16 of 141,977 (0.01%) |
| Matches the SEP-41 token interface | 120 hashes | 1,141 contracts |
| Matches SEP-41 but declares nothing | 109 hashes | 1,127 contracts |

156,173 mainnet contracts were counted, the same number as Stellar's Hubble dataset. 3,083 of 5,261 Wasm hashes are archived under state archival. Two 2,000-contract testnet samples found no declarations at all.

Almost no contract declares what it implements yet. So this project leads with the **interface index**: what a contract's code matches. It tracks declarations as adoption grows, and helps contract authors add them (see [How to help](#how-to-help)).

## Quick start

Requires Go 1.27.1 (the `toolchain` line in `go.mod`). No CGO and no Rust.

```sh
git clone https://github.com/Soroban-CII/soroindex
cd soroindex
go build -o sep47idx ./cmd/sep47idx

# Sample testnet, then seed an index from the sample and list the undeclared gap
./sep47idx phase0 --network testnet --sample 200 --out /tmp/report/
./sep47idx sync   --network testnet --db ./data/testnet.db --seed /tmp/report/testnet-contracts.csv
./sep47idx gap    --network testnet --db ./data/testnet.db --sep 41
```

The `gap` output is always labelled **inferred**: those contracts match the interface but make no claim.

## Architecture

1. `pkg/sepmeta` reads a Wasm module's `contractmetav0` and `contractspecv0` sections, with bounds checks on every length. It parses bytes only and never executes Wasm.
2. `internal/match` compares a contract's function types with a versioned rule file such as [rules/sep-0041.json](rules/sep-0041.json).
3. `internal/ingest` turns network data into facts: instance executables (Wasm hash, Stellar Asset Contract, or a CAP-85 reference), code, and upgrades.
4. `internal/store` is SQLite with migrations, and invariants enforced in SQL.
5. `sep47idx` is the command-line tool. The HTTP API is planned.

Details: [documentation site](https://soroban-cii.github.io/soroindex/).

## Trust tiers

| Tier | Meaning | Networks |
| --- | --- | --- |
| `declared` | The contract's meta lists the SEP (SEP-47) | all |
| `inferred` | Its spec section matches the SEP's rule file | all |
| `verified` | It declares the SEP **and** soroban-guard passed on this exact Wasm hash | testnet only (deferred) |
| `protocol` | A Stellar Asset Contract, which implements SEP-41 by protocol definition | all |

Tiers are never mixed. An inferred match is not a claim, and none of the tiers is a security audit.

## What is built and what is planned

| Built and tested | Planned (tracked as issues) |
| --- | --- |
| SEP-47 meta and spec parser, 4 fuzz targets run 10 min each | Live testnet follow and fixture-upgrade validation |
| SEP-41 rule file and matcher | HTTP API (`/v1/contracts`, `/v1/wasm`, `/v1/stats`, ...) |
| Mainnet census from Hubble; stratified testnet sample | `query`, `contract`, `wasm`, `stats`, `serve` commands |
| SQLite store, migrations, SQL invariants | Docker image, release binaries |
| Seeding from a contract list; stored totals checked against the census | Rule files for SEP-40, SEP-50, SEP-56 |
| Undeclared-gap query; recomputation for new rule versions | Verified tier (deferred until declarations reach 1%) |
| Incremental sync, atomic resume, retention gaps and reference upgrades (offline tests) | |

## How to help

- **Contract authors:** declare what you implement. Add one line to your contract and rebuild:
  ```rust
  soroban_sdk::contractmeta!(key = "sep", val = "41");
  ```
  Several modules may each add their own line; SEP-47 joins them.
- **Maintainers of open-source contracts:** a pull request adding that line to a token contract moves the declared count directly.
- **Contributors:** pick an issue labelled `Stellar Wave`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Prior art and credits

SEP-47 (contract interface discovery) and SEP-48 (contract interface specification) by Leigh McCulloch and the Stellar protocol authors. [SoroTrail](https://github.com/sorotrail/SoroTrail) for event indexing past RPC retention. [soroban-guard](https://github.com/sorobanguard-dev/soroban-guard) for SEP-41 conformance tests, which the verified tier will call. Stellar's [Hubble](https://developers.stellar.org/docs/data/analytics/hubble) dataset for the mainnet census. The full survey of related tools is in [docs/prior-art.md](docs/prior-art.md).

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) for setup, `make` targets and the commit rules. Security issues: [SECURITY.md](SECURITY.md). This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).

## Maintainers

| Name | GitHub | Discord |
| --- | --- | --- |
| ciscokwiz | [@ciscokwiz](https://github.com/ciscokwiz) | `ciscokwiz` |

## Contributors

[![Contributors](https://contrib.rocks/image?repo=Soroban-CII/soroindex)](https://github.com/Soroban-CII/soroindex/graphs/contributors)

## License

[Apache-2.0](LICENSE).
