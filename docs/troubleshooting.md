# Troubleshooting guide

This page lists every sentinel error and common failure mode for soroindex, with real reproductions, exact messages, and concrete fixes.

## Sentinel errors

### `ErrNotWasm` — input is not a valid Wasm v1 module

**Message:** `not a wasm v1 module`

**Reproduction:** Reading a non-Wasm file as contract Wasm.

```go
_, err := ReadCustomSections([]byte("not wasm data"), [], DefaultLimits)
```

**Fix:** Ensure the input bytes begin with the Wasm magic `\0asm` followed by version `1`. Use `ReadCustomSections` only on actual Wasm binaries, not arbitrary bytes.

---

### `ErrTruncated` — length or payload ran past the end of the input

**Message:** `truncated input`

**Reproduction:** Decoding a meta section that is cut short, e.g. a Wasm file whose custom section was stripped or corrupted.

```go
_, err := DecodeMeta(truncatedSection, DefaultLimits)
```

**Fix:** Check that the Wasm file is complete before decoding. If a section is truncated, the error will be returned — handle it gracefully rather than crashing.

---

### `ErrMalformed` — bytes are structurally invalid

**Message:** `malformed input`

**Reproduction:** Decoding an XDR `SCMetaEntry` stream with corrupted data, or a custom section with a non-UTF-8 name.

```go
_, err := DecodeMeta(malformedSection, DefaultLimits)
```

**Fix:** Validate input before decoding. If `ErrMalformed` is returned, the bytes cannot be trusted. Do not attempt recovery — the data is hostile untrusted input.

---

### `ErrLimit` — input exceeded a configured limit, with detail

**Message:** `limit exceeded: <LimitName>`

**Reproduction:** Wasm file exceeds the default 1 MiB maximum, or a custom section exceeds 256 KiB, or there are more than 10,000 sections.

```go
_, err := ReadCustomSections(wasmBytes, []string{"sep"}, Limits{MaxWasmBytes: 1024})
```

**Fix:** Increase the relevant `Limits` value if the Wasm is legitimately large, or truncate/reject oversized input. The error type `*LimitError` matches `errors.Is(err, store.ErrLimit)` — use `errors.Is` to identify which limit was exceeded.

---

### `ErrSchemaTooNew` — database was migrated by a newer binary

**Message:** `database schema is newer than this binary`

**Reproduction:** Opening a database file that was created with a newer version of soroindex.

```go
s, err := store.Open(ctx, "data/mainnet.db", store.Options{Passphrase: networkPassphrase})
```

**Fix:** Upgrade the binary to a newer version that knows the newer schema, or reset the database (delete it and re-run sync from scratch).

---

### `ErrSchemaTooOld` — database schema is older than this binary

**Message:** `database schema is older than this binary; run sync to migrate it`

**Reopening a database that was created by an older soroindex version.**

```go
s, err := store.Open(ctx, "data/mainnet.db", store.Options{Passphrase: networkPassphrase})
```

**Fix:** Run `sep47idx sync` to apply pending migrations. The binary will automatically migrate the schema on first write.

---

### `ErrNetworkMismatch` — database belongs to another network

**Message:** `database network passphrase does not match`

**Reproduction:** Opening a testnet database with a mainnet passphrase (or vice versa).

```go
s, err := store.Open(ctx, "data/testnet.db", store.Options{Passphrase: "Testbot"})
```

**Fix:** Use the correct passphrase for the database's network. The passphrase must match exactly — `Sepnet` for sepnet, `Testnet` for testnet, and the mainnet passphrase for mainnet.

---

### `ErrNotFound` — requested row does not exist

**Message:** `not found`

**Reproduction:** Querying a contract ID that has never been indexed, or a Wasm hash that is not in the database.

```go
_, err := s.Contract(context.Background(), "CZ...") // unknown contract
```

**Fix:** Validate that the contract exists in the index before querying. The API returns HTTP 404 with `{"error":{"code":"not_found","message":"not found"}}` for absent resources.

---

### `ErrReadOnlyUninitialized` — read-only store has no schema

**Message:** `database has no schema; run sync first`

**Reproduction:** Opening a database in read-only mode that has never been synced.

```go
s, err := store.Open(ctx, "data/testnet.db", store.Options{Passphrase: "Testnet", ReadOnly: true})
```

**Fix:** Run `sep47idx sync --db data/testnet.db --rpc-url <url> --start-ledger <n>` first to seed the database.

---

### `ErrSchemaTooOld` (duplicate entry — see `ErrSchemaTooOld` above) — for completeness

Already listed above. Same error, different context (read-write vs read-only open).

---

### `ErrBadQuery` — invalid public query input

**Message:** `invalid query`

**Reproduction:** Supplying an invalid contract ID format or SEP list to an API endpoint.

```bash
curl "http://localhost:8080/v1/contracts?implements=not_a_sep"
```

**Fix:** Validate query parameters before use. The API returns HTTP 400 with `{"error":{"code":"bad_request","message":"invalid query"}}` for bad input.

---

### `ErrBadCheckpoint` — invalid ledger checkpoint

**Message:** `invalid ledger checkpoint`

**Reproduction:** Supplying a corrupted or tampered ledger checkpoint value.

```go
s.SetState(ctx, store.KeyLedgerCheckpoint, "not-a-valid-checkpoint")
```

**Fix:** Ensure the checkpoint is stored in the format `<ledger>:<hash hex>`. If the checkpoint is corrupted, clear it with `sep47idx sync --recompute` or by resetting the database.

---

### `ErrUnsupportedMeta` — unsupported meta version

**Message:** `unsupported meta version`

**Reproduction:** Fetching a Wasm whose `contractmetav0` uses a version the parser does not handle.

```go
_, err := DecodeMeta(wasmSection, Limits{})
```

**Fix:** Ensure the Go toolchain and `modernc.org/sqlite` version are recent enough. The meta version must match what the Soroban toolchain emits.

---

### `ErrUnknownXDR` — ledger metadata uses XDR this build cannot decode

**Message:** `ledger metadata uses XDR this build cannot decode`

**Reproduction:** Syncing a network whose ledger format was introduced after this build's `go-stellar-sdk` version.

```go
_, err := decodeLedgerMeta(data)
```

**Fix:** Update `github.com/stellar/go-stellar-sdk` to a newer version that supports the network's protocol version. Check the go-stellar-sdk changelog for the protocol version where XDR changes land.

---

### `ErrLedgerGap` — ledgers not consecutive

**Message:** `ledgers not consecutive`

**Reproduction:** Syncing from an RPC node whose ledger history has gaps (missed ledgers).

```go
// This can happen when the RPC node is behind or has been reset.
```

**Fix:** Use `--seed` with a Hubble export, or ensure the RPC node has complete ledger history. The sync loop will exit with `ErrRetentionGap` if the resume point is older than the RPC's retained history.

---

### `ErrInvalidRules` — invalid rule file

**Message:** `invalid rule file`

**Reproduction:** Loading a rule file that fails JSON Schema validation or has malformed content.

```go
_, err := LoadRules(rules.FS) // rule file doesn't match schema
```

**Fix:** Validate the rule file against `rules/schema.json`. Ensure `ruleset_version`, `sep`, `source`, `required`, and `optional` fields are correctly formatted.

---

### `ErrUnknownNetwork` — unknown network name

**Message:** `unknown network`

**Reproduction:** Configuring a network name that is not `mainnet`, `testnet`, or `sepnet`.

```go
cfg := config.Common{Network: "unknownnet"}
```

**Fix:** Use only supported network names. Check the `--network` flag or `SEP47IDX_NETWORK` env var.

---

### `ErrNotImplemented` — claim source not implemented

**Message:** `claim source not implemented`

**Reproduction:** Attempting to use `HomeDomainSource`, which is a stub that does not implement contract discovery.

```go
_, err := homeDomainSource.Discover(ctx, wasm, contractRef)
```

**Fix:** `HomeDomainSource` is shipped as a stub with `NeedsInvocation() returning true`. It is **not** registered by default. If you need home domain resolution, implement a custom `ClaimSource` or wait for a future release.

---

## Common failure modes

### Retention gap

**Message:** `resume point is outside RPC retention; seed or backfill the index`

**Reproduction:** Running `sep47idx sync` after the index has fallen behind the RPC node's retained ledger history.

```bash
$ sep47idx sync --db data/testnet.db --rpc-url https://rpc.testnet.stellar.org
sep47idx sync: resume point is outside RPC retention; seed or backfill the index
```

**Fix:** Either:
- Seed the index from Hubble (BigQuery) using `scripts/hubble-export.sql`, or
- Backfill using a full archival RPC node, or
- Start a new database with `--start-ledger` at or after the oldest retained ledger

**Check:** Run `sep47idx stats --network testnet` and verify `lag` is small (under 100 ledgers). If lag is large, you have a retention gap.

---

### Network mismatch

**Message:** `database network passphrase does not match`

**Reproduction:** Opening a database created on testnet with a mainnet binary (or vice versa).

```bash
$ sep47idx sync --db data/testnet.db --rpc-url https://mainnet.stellar.com --network mainnet
sep47idx sync: database network passphrase does not match
```

**Fix:** Use the `--network` flag that matches the database's passphrase, or create a new database for the target network.

---

### Schema version mismatch

**Message:** `database schema is newer/older than this binary`

**Reproduction:** Running a newer binary against an older database, or an older binary against a newer database.

```bash
$ sep47idx sync --db data/mainnet.db ...
sep47idx sync: database schema is newer than this binary
# or
sep47idx sync: database schema is older than this binary; run sync to migrate it
```

**Fix:** Update the binary to a version that matches the database schema, or delete the database and re-sync from scratch.

---

### RPC on another network

**Message:** The RPC endpoint belongs to a different network than the index.

**Reproduction:** Pointing the index at a mainnet RPC while the database is for testnet.

```bash
$ sep47idx serve --db data/testnet.db --rpc-url https://mainnet.stellar.com --network testnet
# May produce incorrect results or error depending on the endpoint
```

**Fix:** Ensure `SEP47IDX_RPC_URL` (or `--rpc-url`) points to an RPC on the same network as `--db`. Use `sep47idx stats` to verify `last_protocol` and `lag` make sense for the network.

---

### Rate limiting

**Message:** `request limit exceeded` (HTTP 429)

**Reproduction:** Making too many requests to the HTTP API in a short period.

```bash
# Default rate limit: 60 requests per minute per IP
for i in {1..70}; do curl "http://localhost:8080/v1/health&network=testnet"; done
```

**Fix:** Increase `--rate-limit` on the serve command, or add a `Retry-After: 60` header and retry after 60 seconds. The default is 60 req/min; set to `0` to disable.

---

### Protocol decode errors

**Message:** `ledger metadata uses XDR this build cannot decode`

**Reproduction:** Syncing a network whose protocol introduced new XDR fields after this build's `go-stellar-sdk` version.

```bash
$ sep47idx sync --db data/testnet.db --rpc-url https://testnet.stellar.com
...
WARN rpc retry method=getNetwork wait=...
```

**Fix:** Update `github.com/stellar/go-stellar-sdk` to a version that supports the network's protocol. Check the go-stellar-sdk CHANGES for protocol version requirements.

---

## Error catalog

| Error code | Message | Category |
|---|---|---|
| `ErrNotWasm` | `not a wasm v1 module` | Wasm parsing |
| `ErrTruncated` | `truncated input` | Wasm parsing |
| `ErrMalformed` | `malformed input` | Wasm parsing |
| `ErrLimit` | `limit exceeded: <name>` | Wasm parsing |
| `ErrSchemaTooNew` | `database schema is newer than this binary` | Database |
| `ErrSchemaTooOld` | `database schema is older than this binary; run sync to migrate it` | Database |
| `ErrNetworkMismatch` | `database network passphrase does not match` | Database |
| `ErrNotFound` | `not found` | Query |
| `ErrReadOnlyUninitialized` | `database has no schema; run sync first` | Database |
| `ErrBadQuery` | `invalid query` | API |
| `ErrBadCheckpoint` | `invalid ledger checkpoint` | Sync |
| `ErrUnsupportedMeta` | `unsupported meta version` | Wasm parsing |
| `ErrUnknownXDR` | `ledger metadata uses XDR this build cannot decode` | RPC |
| `ErrLedgerGap` | `ledgers not consecutive` | Sync |
| `ErrInvalidRules` | `invalid rule file` | Rules |
| `ErrUnknownNetwork` | `unknown network` | Config |
| `ErrNotImplemented` | `claim source not implemented` | Claims |

## Linked from error messages

Where practical, error messages in the HTTP API include references to this troubleshooting page. The API returns machine-readable error codes (`not_found`, `bad_request`, `rate_limited`, `internal`) that users can map to this table.

For the retention gap specifically, the `/v1/health` endpoint exposes `lag` — if `latest_ledger - last_ledger > 200`, check for a retention gap.

---

**Need more help?** Open a GitHub issue or ask on the Stellar developer community.