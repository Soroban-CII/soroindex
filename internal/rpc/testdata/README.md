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
