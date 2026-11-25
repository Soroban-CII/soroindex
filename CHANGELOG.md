# Changelog

## Unreleased

- Add `sepmeta.ReadCustomSectionsAt`, which reads custom sections from an
  `io.ReaderAt` without loading the module. It returns exactly what
  `ReadCustomSections` returns for the same bytes, checked by a differential
  test over every fixture and inside `FuzzReadCustomSections`. On a 1 MiB
  module it allocates the requested payloads plus under 16 KiB (#62).
- Report which SEP-41 version a Wasm's interface implements. `rules/history/` adds
  SEP-41 v0.1.0 and v0.3.0, each citing its stellar-protocol commit. Matches carry
  `matched_version` in the API and in the `wasm`, `contract` and `inspect` output.
  Stored statuses are unchanged (#78).
- Refuse to sync after a testnet reset with `ErrNetworkReset`, before writing
  anything. Each batch stores a `ledger_checkpoint` hash, and sync compares it
  with the RPC. A stored ledger more than 17,280 ledgers ahead of the RPC tip
  also counts as a reset (#79).
- Store the protocol version of the last indexed ledger (`last_protocol`), log
  protocol changes, and report the protocol in `stats`, `/v1/stats` and
  `/v1/health`. Metadata the pinned SDK cannot decode now stops sync with
  `ErrUnknownXDR`, naming the ledger, the protocol and the SDK version (#80).
- Fetch and parse Wasm on a bounded pool sized by `--rpc-concurrency`, and prepare
  seed batches in parallel. Commits stay in order, one transaction per batch.
  Seeding 2,000 contracts through a fake RPC with 100 ms per call went from
  1.55 s to 0.46 s (#81).
- `stats --lag` reads the RPC's network and latest ledger, refuses a provider
  on another network, and reports latest_ledger and lag in JSON plus a "Sync:"
  line in text. An RPC behind the index is an error, not a negative or wrapped
  lag. (#153)
- Under each "Inferred SEP-N" line, `sep47idx wasm` now prints function names
  and signatures for missing and mismatched functions, so a contract author
  can see what to fix without rerunning with --json. (#153)
- `.github/workflows/release.yml` adds a tag-triggered release workflow that
  builds static archives, verifies checksums, builds a GHCR image and creates a
  draft release for maintainer review. (#153)
- New CLI and API tests cover sync retention-gap detection, wasm human output
  fix lines, contract history across several upgrades, and snapshot every API
  endpoint against fixture data. (#153)

- Add `GET /v1/contracts?ids=...`, a bulk lookup of up to 100 contract IDs that
  reports unknown IDs in `not_found` and charges the rate limit per 10 IDs (#114).
- Add `sep47idx inspect <file.wasm>`, which runs the indexer's analysis on a local
  file with no database or RPC, with `--json` and `--require-match` (#115).
- Add `GET /v1/stats/toolchains`, the `rsver`, `rssdkver` and `cliver`
  distribution with declaration and SEP-41 match rates by hash and by contract (#113).
- Snapshot every API endpoint over a fixture index built from recorded ledgers
  and the golden Wasm, and generate the docs/api.md fixture examples from them (#112).
- Fix `sep47idx wasm` printing a memory address instead of the anomaly code on
  declaration lines.

- Build with Go 1.27.4 for GO-2026-6859 (net/http `MaxBytesReader` header race in
  the HTTP/1 server), reported by the security workflow on 16 November 2026.

## v0.1.0 — 9 November 2026

Released from commit `3abe5a3`. The release candidate was first cut on 9 October 2026.

- Decode Protocol 30 ledgers. Testnet moved to Protocol 30 (CAP-0088
  millisecond close times), and from ledger 5,620,968 incremental sync stopped
  on `'3' is not a valid StellarValueType`. go-stellar-sdk is now v0.8.0, which
  adds the `SIGNED_MS` and `EMPTY_TX_SET_MS` header arms. A regression test
  decodes recorded testnet ledgers carrying both arms.
- Update the transitive `klauspost/compress` dependency to v1.20.3 for
  GO-2026-6865. No 1.18 release has the fix.
- Parse SEP-47 metadata and SEP-48 interface specifications without executing Wasm.
- Infer SEP-41 interfaces using reviewed versioned rules; keep declared, inferred,
  protocol and deferred verified evidence separate.
- Seed and synchronize SQLite indexes with atomic resume, retention-gap detection,
  archival, upgrades and CAP-85 reference fan-out.
- Query the index through the read-only HTTP API and CLI, with keyset pagination,
  JSON/CSV output and optional per-IP request limiting.
- Page through the inferred undeclared gap with bounded reads and resumable
  cursors, preserving ruleset and parser provenance.
- Recover testnet sampling when RPC retention advances, with bounded retries
  and explicit reporting of expired planned ledger ranges.
- Provide static linux/darwin amd64/arm64 builds and a non-root distroless Dockerfile.
- Record the dated mainnet adoption census and real testnet validation evidence.
- Use Go 1.27.3 and the patched compression dependency; enable security and nightly
  testnet integration workflows. The compiler patch was refreshed on 26 October
  2026 to address GO-2026-6809 before publication.

The GitHub release carries the four static archives, `SHA256SUMS` and
`BUILD.json`. No container image, hosted API or verifier run is published. The
deployed-fixture live upgrade check was waived; offline tests cover upgrade
behavior.
