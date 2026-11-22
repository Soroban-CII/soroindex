# Recorded RPC responses

Captured from `https://soroban-testnet.stellar.org` (protocol 29) on
2026-10-07 with curl. They are replayed by `client_test.go`; tests never
touch the network.

| File | Request |
| --- | --- |
| `getNetwork.json` | `getNetwork` |
| `getLatestLedger.json` | `getLatestLedger`. The 551,472-character `metadataXdr` field was removed to keep the repo small; the client does not read it. Nothing else was changed. |
| `getLedgers.json` | `getLedgers` with `startLedger` 5062907 and `limit` 2. Unmodified (401,638 bytes); its `metadataXdr` values are real `LedgerCloseMeta`. |
| `getLedgers_out_of_range.json` | `getLedgers` with `startLedger` 1, outside retention: JSON-RPC error -32600. |

## Ledger corpus for change extraction tests

`getLedgers.json` contains two consecutive ledgers (5062907, 5062908) that exercise a subset of the change types. The `TestRecordedTestnetLedgers` test in `internal/ingest/changes_test.go` asserts the exact facts extracted from each.

| Ledger | Sequence | Change types covered | Notes |
| --- | --- | --- | --- |
| 1 | 5062907 | Instance create/update (11 instances) | No Wasm deploy, no exec refs, no archival/restoration |
| 2 | 5062908 | Wasm deploy (1 code created), Instance create/update (6 instances) | No exec refs, no archival/restoration |

### Missing change types (to be recorded from testnet)

The following change types are not yet covered by the committed corpus. To add them, run `getLedgers` against `https://soroban-testnet.stellar.org` with appropriate `startLedger` values until a ledger containing the desired change appears, then save the response as a new JSON file and add a test case.

| Change type | What to look for | Example ledger search strategy |
| --- | --- | --- |
| SAC creation | Instance with `KindSAC` | Scan recent ledgers for `ContractExecutableStellarAsset` |
| CAP-85 exec ref create | `ExecRefs` with a new `(owner, tag)` pair | Scan for `ScvExecutableTag` key in `ContractData` |
| CAP-85 exec ref update | `ExecRefs` where `WasmHash` differs from prior | Same as above; compare consecutive ledgers |
| Archival (eviction) | `Evicted` non-empty | Scan for `EvictedKeys` in `LedgerCloseMeta` |
| Restoration | `Restored` change kind | Scan for `LedgerEntryChangeTypeLedgerEntryRestored` |

When a new ledger is recorded, add its file to this directory, update this table, and add a test case in `internal/ingest/changes_test.go` asserting the exact extracted facts.
