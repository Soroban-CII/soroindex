# Changelog

## Unreleased

- Make the parser and XDR limits configurable: `--max-wasm-bytes` (1MiB,
  hard cap 64MiB), `--max-section-bytes` (256KiB, capped by the module size),
  `--max-sections` (10000, capped by what a module of that size can hold),
  `--max-xdr-depth` (500, hard cap 1500, the pinned decoder's own maximum) and
  `--max-xdr-base64-bytes` (64MiB, hard cap 256MiB, the largest RPC response
  body the client reads), each with a `SEP47IDX_*` variable and a size suffix
  such as `4MiB`. Values are validated at startup, before any network call or
  untrusted byte, and a zero is rejected rather than treated as a default. The
  limits each sync ran with are stored in `sync_state` and reported by
  `sep47idx stats`; `/v1/stats` is unchanged. Defaults are unchanged, so
  shipped behaviour is the same (#139).

- Add a conformance suite against stellar-cli 30.0.1 and the Python
  stellar-sdk 17.0.1. `scripts/record-conformance.sh` records both tools'
  output for every fixture and vector under `testdata/conformance/`, and
  `TestConformanceWithStellarCLIAndPythonSDK` checks `pkg/sepmeta` against
  it. The eight intentional differences are listed in `docs/how-it-works.md` (#63).
- Add structure-aware fuzz targets `FuzzDecodeSpecStructured` and
  `FuzzDecodeMetaStructured`, which build valid spec and meta streams from
  fuzzer choices and check decode, re-encode, `Functions` and truncation
  invariants. `DecodeMeta` and `DecodeSpec` now report nesting past
  `MaxXDRDepth` as `ErrLimit` rather than `ErrMalformed`, and no longer
  misreport a cut entry as malformed under a small `MaxXDRDepth`, a bug the
  new spec target found (#64).
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
