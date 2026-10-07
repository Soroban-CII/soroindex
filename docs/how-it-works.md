# How it works

## Reading a contract

A Soroban contract's Wasm carries two custom sections the index reads:

- `contractmetav0`: key/value meta entries. SEP-47 uses the key `sep`, with a comma-separated list of SEP numbers. Several `sep` entries are normal: each Rust module may add its own, and SEP-47 joins them.
- `contractspecv0`: the interface. Every function with its parameter and return types.

`pkg/sepmeta` reads both. It treats the bytes as hostile: every length is checked against the bytes remaining and a configured limit before anything is allocated, and malformed input is an error, never a crash. Four fuzz targets ran 10 minutes each on 7 October 2026 without a failure. The index never executes Wasm.

Tokens that do not follow SEP-47's format are kept, not dropped: `041` and `SEP-41` count as 41 in the default lenient mode, with a `leading_zero` or `prefixed_token` anomaly recorded. `abc`, empty tokens, and values over 4 KiB produce no SEP and are recorded as anomalies.

## Where a contract's code comes from

A contract instance names its executable in one of three ways:

1. a Wasm hash;
2. the Stellar Asset executable (a SAC);
3. since protocol 28 (CAP-85), an **external reference** to a Wasm hash held in another contract's storage entry.

For an external reference, the index resolves the hash through the owner's entry. When that entry changes, every contract that references it is upgraded at once, though none of their own entries changed. The index tracks that.

## Building an index

Today an index is built by **seeding** from a list of contract IDs (`sync --seed`): the Hubble export, a Phase 0 sample, or any list. For each contract the index reads the current instance, fetches each Wasm once, and stores claims, anomalies and interface matches in SQLite. Every write is idempotent: seeding twice leaves every table byte-identical.

A seeded version row starts at the instance's last-modified ledger. Earlier history is unknown and not claimed.

## Archival

Stellar archives entries whose TTL passes. On 7 October 2026, 3,083 of 5,261 mainnet Wasm hashes and 74,959 of 156,173 mainnet contract instances were archived. The index marks archived code and instances. It never deletes claims because an entry was archived; it keeps the last known data.

## Planned: incremental sync

The sync loop will follow the network with `getLedgers`, 200 ledgers per batch, each batch one transaction. It will detect upgrades and archival as they happen. If the index falls behind the RPC node's retention window, it will stop with an error rather than skip ledgers. The ledger reader that enforces "never skip" is built (`internal/ingest/ledgers.go`); the loop around it is tracked as an issue.
