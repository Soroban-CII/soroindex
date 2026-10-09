# CLI reference

The binary is `sep47idx`. Historical outputs below were captured on 7 October 2026. The help output was refreshed from a local build on 8 October 2026.

Every flag also has an environment variable: `SEP47IDX_` plus the flag name in capitals, dashes as underscores (`--rpc-url` is `SEP47IDX_RPC_URL`). A flag on the command line overrides the variable. Exit codes: `0` ok, `1` error, `2` not found.

```text
$ sep47idx help
usage: sep47idx <command> [flags]

commands:
  gap        list contracts that match a SEP's interface but declare nothing (inferred)
  phase0     measure SEP-47 adoption and write the adoption report
  sync       seed the index, sync ledgers or recompute matches
  version    print the binary version
```

## Common flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--network` | `testnet` | `mainnet` or `testnet`. The RPC's passphrase must match it. |
| `--rpc-url` | testnet: `https://soroban-testnet.stellar.org`; mainnet: none | Stellar RPC endpoint. Mainnet has no default: SDF runs no public mainnet RPC. |
| `--db` | `./data/<network>.db` | SQLite database. One file per network. |
| `--json` | off | JSON output. |
| `--rpc-timeout` | `30s` | Timeout per RPC attempt. |
| `--rpc-concurrency` | `4` | RPC requests in flight. |

## phase0

Measures adoption across a network and writes `report/ADOPTION.md`, `<network>-summary.json`, `<network>-raw.csv` and `<network>-contracts.csv`.

```text
$ sep47idx phase0 --network mainnet --rpc-url https://soroban-rpc.mainnet.stellar.gateway.fm \
    --hashes report/mainnet_hashes.csv --instances report/mainnet_instances.csv --out report/
mainnet: 156173 contracts (4042 SAC, 152131 wasm, 0 wasm_ref); 2178 hashes fetched, 3083 archived; declares any SEP: 13/2178 hashes (0.60%), 16/141977 contracts (0.01%); SEP-41 match 120 hashes; undeclared gap 109 hashes
wrote report/
```

```text
$ sep47idx phase0 --network testnet --sample 2000 --out report/
testnet: 2000 contracts (112 SAC, 1842 wasm, 9 wasm_ref); 479 hashes fetched, 26 archived; declares any SEP: 0/479 hashes (0.00%), 0/1775 contracts (0.00%); SEP-41 match 17 hashes; undeclared gap 17 hashes
wrote report/
```

`--verify-db <db>` compares a summary with a database seeded from the same contracts, and exits 1 on any difference:

```text
$ sep47idx phase0 --network testnet --out report/ --verify-db s20.db
check                                    phase0      store
live SAC contracts                          112        112  ok
live wasm contracts                        1842       1842  ok
measured contracts                         1775       1775  ok
hashes run by a measured contract           479        479  ok
SEP-41 match, contracts                     100        100  ok
...
```

Other flags: `--sample`, `--strata`, `--page-size` (testnet); `--exec-refs` (mainnet, CAP-85 references); `--partial-threshold`; `--rules <dir>`; `--render-only`; `--record-instance-archival <db>`.

## sync

Seeds an index, recomputes interface matches, or applies consecutive ledger batches. With no seed or recompute option, it resumes from the database’s `last_ledger`. A new index requires a seed or `--start-ledger`.

| Flag | Meaning |
| --- | --- |
| `--start-ledger` | First ledger to index when the database has no resume point. An existing resume point takes precedence. |
| `--follow` | Keep polling after catching up. Combine with a seed to seed and then follow. |
| `--interval` | Positive polling duration, default `5s`. |

Each batch and its resume point commit in one transaction. A retention gap exits with an error and tells the operator to seed or backfill. Progress is JSON on stderr, including `last_ledger`, `latest_ledger`, `lag` and `caught_up`. The 30-minute testnet follow check passed on 9 October 2026 with a final lag of one ledger. The operator waived the deployed-fixture upgrade check. Offline tests exercise resume, rollback, upgrades and reference changes.

```text
$ sep47idx sync --network testnet --db s20.db --seed report/testnet-contracts.csv
seeded 2000 of 2000 contracts (0 not found, 37 archived, 0 hash hints differed); fetched 514 wasm; last_ledger 5066294
```

```text
$ sep47idx sync --network testnet --db smoke2.db --recompute
recompute: 0 recomputed, 0 no_spec, 0 need a re-fetch (analyzed before migration 0002), 0 archived before parsing
```

The seed file needs a `contract_id` column (other columns are ignored), or no header and one contract ID per line. `--allow-invocation` exists for claim sources that simulate contract calls; none ships enabled.

## gap

Lists contracts whose Wasm matches a SEP's interface and that declare nothing. The output is always labelled inferred.

```text
$ sep47idx gap --sep 41 --network testnet --db smoke.db
Undeclared gap for SEP-41 (inferred: interface matches sep41-v0.5.2, no SEP declared): 1 contracts
CDH4BTGP7GRDFO6Y24AP4RP34NMC3V2RXTTUA4ABCFRMFEYWR6AG6GK6  a5f6b06ca8fb10a7f3d12ba5fae3361f04a242054757080cf14c375b88e1c898
```

A SEP without a rule file exits 2:

```text
$ sep47idx gap --sep 50 --network testnet --db smoke.db
sep47idx gap: no rule file for SEP-50, so no interface can be inferred
```

## version

```text
$ sep47idx version
sep47idx dev (go1.27.1, darwin/arm64)
```

## Planned commands

`query`, `contract`, `wasm`, `stats`, `serve` and `verify` are designed but not built. Each is tracked as an issue.
