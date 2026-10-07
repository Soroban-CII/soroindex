# Self-hosting

There is no Docker image or release binary yet; both are tracked as issues. Build from source.

## Build

Go 1.27.1, with `CGO_ENABLED=0`; SQLite is the pure-Go `modernc.org/sqlite` driver.

```sh
git clone https://github.com/Soroban-CII/soroindex
cd soroindex
CGO_ENABLED=0 go build -o sep47idx ./cmd/sep47idx
```

## Configure

Every flag can come from an environment variable (see [CLI reference](cli.md)):

| Variable | Use |
| --- | --- |
| `SEP47IDX_NETWORK` | `mainnet` or `testnet` |
| `SEP47IDX_RPC_URL` | RPC endpoint; required for mainnet |
| `SEP47IDX_DB` | Database path |
| `SEP47IDX_RPC_TIMEOUT`, `SEP47IDX_RPC_CONCURRENCY` | RPC tuning |

Stellar's docs list mainnet RPC providers. Several public endpoints answered a 200-key `getLedgerEntries` call correctly on 7 October 2026, among them `https://soroban-rpc.mainnet.stellar.gateway.fm`, which the mainnet census used.

## Seed

An index starts from a seed: a CSV with a `contract_id` column. For mainnet, export Q2 of [`scripts/hubble-export.sql`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/hubble-export.sql) from BigQuery, or run `phase0` and use its `mainnet-contracts.csv`.

```sh
sep47idx sync --network mainnet --rpc-url <url> --seed mainnet-contracts.csv
```

On 7 October 2026 a seed of all 156,173 mainnet contracts through a public endpoint ran about 1.4 seconds per batch of 200 contracts; a resumed run over the same list took 18 minutes, and the database was 85 MB. Seeding is idempotent; if it stops, run it again.

## Backups

The index is one SQLite file per network in WAL mode. Back it up with `sqlite3 data/mainnet.db ".backup backup.db"` while the indexer runs, or copy the `.db` file while it is stopped. A database records its network passphrase and refuses to open for another network.
