# Prior art

Searched on 7 October 2026. This page records what already exists near this
project and how each differs. Every entry was checked against its source code or
README on that date, not only its description.

**Result: no project found does network-wide SEP-47 indexing.** Every tool that
reads a contract's `sep` meta does so for one contract the user names. No tool
answers "list every contract that declares SEP-41". The closest neighbours are
explorers that index every contract but never read the `sep` key, and
single-artifact inspectors that read the key but never scan the network.

## Search method

GitHub repository, code, and issue search for each term:

| Term | Search | Relevant result |
| --- | --- | --- |
| `SEP-47` | repos, issues (incl. label `Stellar Wave`) | Repos: none. Issues: one open SDK issue (Beans-BV, below); no Wave issue about SEP-47 indexing. |
| `sep47` | repos, Wave issues | none |
| `contractmetav0` | repos, code, Wave issues | Code: 25 files across SDKs, explorers, verifiers and docs; each reads one contract or documents the section. Issues: 8, all single-contract meta parsing. |
| `"contract meta"` | repos, code (with `sep soroban`) | Repos: Ethereum, Koinos and Everscale meta stores only. Code: no hits. |
| `"interface discovery"` | repos, issues | Issues: SaboLabs/soroban-devkit (single-contract `inspect`), GasGuard (below). |
| `implements=` | code (`implements= soroban`, `implements=41`) | No Soroban hits; only unrelated ANTLR grammar files. |
| `soroban indexer` | repos | 9 repos, all event or transaction indexers. |
| `supported_seps` | code | 20 files: the Python SDK, SDK compatibility matrices, one anchor table. |

The StellarExpert public API was also queried directly (below).

## Projects named in the brief

### StellarFoundry/stellar-contract-platform ("Stellar Contract Observatory")

Rust toolkit, created 2026-09-22. Inspects **one Wasm artifact** offline:
sections, `contractspecv0` interface model, interface diff and compatibility
verdict, artifact and interface fingerprints, deployed-hash comparison. It reads
`contractmetav0` only as build metadata, to compare two builds. It has no
`sep` parsing, no network scan and, per its README, no live RPC transport yet.

**Difference:** single-artifact and offline. Its interface diff is related to
how this project tracks inferred status across upgrades, but it keeps no index
and records no declared tier.

### SoroTrail (sorotrail/SoroTrail, SoroLens, SoroBeacon)

Go contract **event** indexer: it stores events past RPC retention and serves
them as JSON. SoroLens is its explorer UI and SoroBeacon its alerting layer. It
parses Wasm only to decode event shapes from the spec
(`internal/spec/wasm_parsing_test.go`).

**Difference:** events, not interfaces. Event indexing is a non-goal here.
SoroTrail's retention-window handling is a useful reference for §5.9.

### soroban-guard (npm `soroban-guard`, sorobanguard-dev/soroban-guard)

Conformance test runner for **one deployed SEP-41 token**: `soroban-guard
<contract-id> --format json`. It has 16 checks, each reported as pass, fail or
`UNVERIFIABLE`, and three exit codes. Version 0.4.2 was published 2026-09-30.

**Difference:** it tests one contract on request. This project calls it for the
`verified` tier (§5.13) and does not reimplement it.

Not to be confused with Veritas-Vaults-Network/Soroban-Guard-Core, a static
vulnerability analyzer that has a similar name but is unrelated.

### Stardex (stardexhq/Stardex)

Indexer and data API that records every contract event, with resumable
ingestion and signed webhooks.

**Difference:** events and history. It reads no meta and builds no interface
index.

### Sierpe (sierpeproject/sierpe)

Self-hosted indexer: you **register** your contracts and it backfills their
event and state history into your Postgres. Its roadmap lists "contract-class
discovery: register a wasm hash, index every contract deployed from it".

**Difference:** registration-based and event-focused. The roadmap item groups
contracts by Wasm hash, as this project's `wasm` table does, but for event
collection, not declared or inferred SEP support.

### Python stellar-sdk `ContractMeta.supported_seps()` (StellarCN/py-stellar-base)

Version 16.1.0, released 2026-09-03. Parses `sep` meta from **one Wasm**: it
splits on `,`, strips whitespace, deduplicates in first-seen order and skips
invalid tokens (or raises when `strict=True`).

**Difference:** a per-contract library. Two behaviours differ from this
project's §5.2 rules, and both are noted for the parser vectors:

- `"041"` is accepted as 41 with no anomaly recorded. Here, lenient mode
  accepts it and records `leading_zero`, and strict mode drops it.
- `str.isdecimal()` accepts non-ASCII decimal digits, so `"٤١"`
  (Arabic-Indic digits) parses as 41. Here it is `non_numeric`.

## Other relevant work found

| Project | What it does | Why it is not this project |
| --- | --- | --- |
| StellarExpert explorer (stellar-expert/stellar-expert-explorer, contract-wasm-interface-parser) | Lists every contract on the network. The contract page parses `contractmetav0` and the spec **in the browser** for that one contract. | The public API (`/explorer/public/contract`) has no SEP filter. `?sep=41` and `?search=sep` return the same records as no filter. Its OpenAPI document has no contract-meta query. |
| Stellar CLI v28.1.0 (`stellar contract info meta`) | Prints the meta of one contract or Wasm. | One contract at a time, as CLAUDE.md §1 states. |
| Stellar Lab (stellar/laboratory) | Contract Explorer shows `contractmetav0` for one contract. | One contract at a time. |
| Soneso SDKs (Flutter, iOS/macOS, PHP, KMP), Java SDK, Beans-BV .NET SDK (open issue) | SEP-46/47 meta parsing, published as SEP-0047 compatibility matrices. | Libraries for one contract. |
| ANTAPEX/Soroban-Registry | Package registry. Its indexer detects every `createContract` operation (contract ID, deployer). Search uses publisher-supplied categories and source verification. | Never reads `sep` meta. Classification is self-registered, a non-goal here (§1). |
| Soroban-Smart-Block-Explorer backend | Transaction and event explorer. `wasm-abi-extractor.ts` labels a contract SEP-41 when 4 of 6 export names match. That list includes `total_supply`, which is not in SEP-41. | Heuristic on names only, with no types and no `sep` meta. Its `supported_seps` column belongs to an **anchor** table (stellar.toml), not contracts. |
| MDTechLabs/GasGuard, issue "Contract Interface Registry" | Proposes a registry where interfaces are registered, with lookup APIs. | Self-registration, a non-goal here. |
| blockchain-maxis/signet | Developer profiles. It indexes contracts deployed by linked wallets and parses `contractmetav0` for build provenance. | Scoped to one developer's deployments, with no SEP index. |
| Galmanus/sorohunter | Adversarial missing-auth probing by local fork simulation. | Executes contracts, which this project never does. |
| SEP-55, SEP-58 (Draft 0.6.0, updated 2026-07-15) | Build verification by CI attestation (55) and build reproducibility vocabulary (58). | Source and build verification are non-goals here (§1). |

## What this project adds

1. A **network-wide** scan: every contract and every Wasm hash, seeded from
   Hubble and kept current from `getLedgers`.
2. The **declared** tier from SEP-47 meta, with anomalies recorded rather than
   silently dropped.
3. An **inferred** tier from a typed check of `contractspecv0` against a
   versioned rule file, which also finds contracts that match an interface
   without declaring it.
4. A **verified** tier on testnet that reuses soroban-guard.
5. Tracking of all three across **upgrades**.
