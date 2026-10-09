# soroindex

A public index of Soroban smart contracts by the SEPs they declare, the interfaces they match, and how both change on upgrade.

[![CI](https://github.com/ciscokwiz/soroindex/actions/workflows/ci.yml/badge.svg)](https://github.com/ciscokwiz/soroindex/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/Soroban-CII/soroindex/pkg/sepmeta.svg)](https://pkg.go.dev/github.com/Soroban-CII/soroindex/pkg/sepmeta)
[![Release](https://img.shields.io/github/v/release/ciscokwiz/soroindex)](https://github.com/ciscokwiz/soroindex/releases)

A public index of Soroban smart contracts that answers "which contracts are tokens?" and similar questions. For every deployed contract it records what the contract says it implements, what its code interface actually matches, and reserves a separate testnet tier for future conformance results. It tracks how those answers change when a contract is upgraded. Anyone can query it through an HTTP API or a command-line tool.

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

The v0.1.0 candidate is built and tested locally; release and registry publication await operator review. Build the Docker image from source for now. Requires Go 1.27.2 for seeding and Docker; no Rust or CGO.

```sh
git clone https://github.com/ciscokwiz/soroindex
cd soroindex
go build -o sep47idx ./cmd/sep47idx
mkdir -p data
./sep47idx phase0 --network testnet --sample 200 --out /tmp/soroindex-report
./sep47idx sync --network testnet --db ./data/testnet.db --seed /tmp/soroindex-report/testnet-contracts.csv
docker build --build-arg VERSION=v0.1.0 -t soroindex:v0.1.0 .
docker run --rm --read-only -p 127.0.0.1:8080:8080 \
  --mount "type=bind,src=$(pwd)/data,dst=/data,readonly" \
  soroindex:v0.1.0 serve --network testnet --db /data/testnet.db --addr :8080
```

In another terminal:

```sh
curl --fail 'http://127.0.0.1:8080/v1/contracts?implements=41&tier=inferred&limit=1'
```

Use `tier=declared` to query metadata claims separately. The sample may contain no declarations. Serving reads the database and does not run sync. See [Self-hosting](docs/self-hosting.md) for continuous ingestion and deployment.

## Architecture

1. `pkg/sepmeta` reads a Wasm module's `contractmetav0` and `contractspecv0` sections, with bounds checks on every length. It parses bytes only and never executes Wasm.
2. `internal/match` compares a contract's function types with a versioned rule file such as [rules/sep-0041.json](rules/sep-0041.json).
3. `internal/ingest` turns network data into facts: instance executables (Wasm hash, Stellar Asset Contract, or a CAP-85 reference), code, and upgrades.
4. `internal/store` is SQLite with migrations, and invariants enforced in SQL.
5. `sep47idx` is the command-line tool. Its read-only HTTP API exposes the same stored evidence and tier filters.

Details: [source documentation](docs/index.md). The [documentation site](https://ciscokwiz.github.io/soroindex/) awaits the operator’s Pages deployment.

## Trust tiers

| Tier | Meaning | Networks |
| --- | --- | --- |
| `declared` | The contract's meta lists the SEP (SEP-47) | all |
| `inferred` | Its spec section matches the SEP's rule file | all |
| `verified` | It declares the SEP **and** soroban-guard passed on this exact Wasm hash | testnet only (deferred) |
| `protocol` | A Stellar Asset Contract, which implements SEP-41 by protocol definition | all |

Tiers are never mixed. An inferred match is not a claim, and none of the tiers is a security audit.

## Prior art and credits

SEP-47 (contract interface discovery) and SEP-48 (contract interface specification) by Leigh McCulloch and the Stellar protocol authors. [SoroTrail](https://github.com/sorotrail/SoroTrail) for event indexing past RPC retention. [soroban-guard](https://github.com/sorobanguard-dev/soroban-guard) for SEP-41 conformance tests, which the verified tier will call. Stellar's [Hubble](https://developers.stellar.org/docs/data/analytics/hubble) dataset for the mainnet census. The full survey of related tools is in [docs/prior-art.md](docs/prior-art.md).

## How to help

- **Contract authors:** declare what you implement. Add one line to your contract and rebuild:
  ```rust
  soroban_sdk::contractmeta!(key = "sep", val = "41");
  ```
  Several modules may each add their own line; SEP-47 joins them.
- **Maintainers of open-source contracts:** a pull request adding that line to a token contract moves the declared count directly.
- **Contributors:** pick an issue labelled `Stellar Wave`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Contributing

The [Wave application package](docs/wave/APPLICATION.md) distinguishes built features from the 25 remaining issues and publication tasks. Read [CONTRIBUTING.md](CONTRIBUTING.md) for setup, `make` targets and the commit rules. Security issues: [SECURITY.md](SECURITY.md). This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).

## Maintainers

| Name | GitHub | Discord |
| --- | --- | --- |
| ciscokwiz | [@ciscokwiz](https://github.com/ciscokwiz) | `ciscokwiz` |

## Contributors

[![Contributors](https://contrib.rocks/image?repo=ciscokwiz/soroindex)](https://github.com/ciscokwiz/soroindex/graphs/contributors)

## License

[Apache-2.0](LICENSE).
