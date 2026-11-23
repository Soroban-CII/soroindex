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

Seeding overlaps its RPC round trips. A pool of `--rpc-concurrency` workers (default 4) prepares batches of 200 contracts: it reads instances, resolves references, and fetches and parses each new Wasm once. It runs at most twice the pool size ahead of the writer. Batches still commit one transaction at a time, in file order. A failed or cancelled batch stops the pool, and nothing after it is committed. `last_ledger` is written only after every batch has committed. Incremental sync uses the same pool to fetch and parse a batch's new Wasm before the batch's transaction opens.

## Archival

Stellar archives entries whose TTL passes. On 7 October 2026, 3,083 of 5,261 mainnet Wasm hashes and 74,959 of 156,173 mainnet contract instances were archived. The index marks archived code and instances. It never deletes claims because an entry was archived; it keeps the last known data.

## Incremental sync

The sync loop reads `getLedgers` in batches of up to 200 ledgers. It applies instance, code, reference and archival changes in network order. Every batch and its `last_ledger` commit in one transaction, so a failed batch can be resumed. Reference updates upgrade the contracts that currently reference that owner and tag. Unresolved or archived references have no current claims.

Each committed batch also records, in the same transaction, the protocol version of its last ledger (`last_protocol`) and that ledger's hash (`ledger_checkpoint`). `stats` and `/v1/health` report `last_protocol`, and sync logs a `protocol change` record when it changes.

A seeded index resumes with `sync`; `--follow` polls after catching up. An index without a seed needs `--start-ledger`. If its next ledger is older than the RPC retention window, the loop returns an error before applying a batch. It rechecks the latest ledger before reporting `caught_up`.

### Protocol upgrades

When a protocol upgrade adds XDR that the pinned go-stellar-sdk cannot decode, sync stops with `ErrUnknownXDR`. The message names the ledger, the protocol it closed under (read from the raw header bytes), and the pinned SDK version. Here is the message that `TestUnknownXDRNamesLedgerProtocolAndSDK` produces. It takes recorded testnet ledger 5062907 and patches its `StellarValueType` to 7 (captured 23 November 2026):

```text
ledger metadata uses XDR this build cannot decode: ledger 5062907, protocol 29: upgrade github.com/stellar/go-stellar-sdk (pinned v0.8.0) to a version that supports protocol 29: decoding LedgerCloseMetaV2: decoding LedgerHeaderHistoryEntry: decoding LedgerHeader: decoding StellarValue: decoding StellarValueExt: decoding StellarValueType: '7' is not a valid StellarValueType enum value
```

Nothing from that batch is written. Upgrade the SDK (its own commit with a test run), rebuild, and run `sync` again. It resumes at the same ledger.

### Network resets

Testnet and futurenet are reset from time to time. Ledger numbers restart under the same passphrase. An index built before a reset describes contracts that no longer exist. Sync refuses to continue, with `ErrNetworkReset`, before it writes anything, when either:

- the stored `last_ledger` is more than 17,280 ledgers (about a day) ahead of the RPC's latest ledger. A node a few ledgers behind gets the ordinary "stored ledger exceeds RPC latest" error instead; or
- the RPC still retains the checkpoint ledger, but its hash differs from the stored `ledger_checkpoint`. This catches a reset even after the new network has passed the old ledger number.

What to do: keep the old file if you want its history, and start a new index in a new `--db` file, seeded or with `--start-ledger`. Do not point sync at the old file again. A seed-only index has no checkpoint until its first sync batch commits, so only the ledger-number check applies to it.

These paths have offline tests. The 30-minute testnet follow check passed on 9 October 2026 with a final lag of one ledger. The operator waived the deployed-fixture upgrade check.

See the [Stage F validation evidence](stage-f-progress.md) for the completed offline and live-follow checks and the explicit fixture waiver.
