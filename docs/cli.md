# CLI reference

The binary is `sep47idx`. Historical outputs below were captured on 7 October 2026. The help output and new command captures were refreshed from a Linux build on 9 October 2026 (Go 1.27.2).

Every flag also has an environment variable: `SEP47IDX_` plus the flag name in capitals, dashes as underscores (`--rpc-url` is `SEP47IDX_RPC_URL`). A flag on the command line overrides the variable. Exit codes: `0` ok, `1` error, `2` not found.

```text
$ /workspace/scratch/stage-g/sep47idx help
usage: sep47idx <command> [flags]

commands:
  contract   show a contract's claims and optional full history
  gap        list contracts that match a SEP's interface but declare nothing (inferred)
  phase0     measure SEP-47 adoption and write the adoption report
  query      query current contracts by SEP and trust tier
  serve      serve the index through the read-only JSON API
  stats      show stored adoption gap anomalies and sync progress
  sync       seed the index, sync ledgers or recompute matches
  version    print the binary version
  wasm       show parsed Wasm metadata and interface evidence
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

## query

Read-only current contract queries share the API's AND/OR and tier semantics.
`--implements` is AND; `--implements-any` is OR. `--tier` defaults to declared.
`--kind`, `--limit` (1..500, default 50) and `--cursor` match the API. JSON includes
`next_cursor`; human and CSV output write continuation information to stderr.
`--csv` and `--json` cannot be combined. Environment values follow the same flag rules.

```text
$ /workspace/scratch/stage-g/sep47idx query --db /workspace/scratch/stage-g/testnet.db --implements 41 --tier inferred --limit 1 --json
{
  "contracts": [
    {
      "contract_id": "CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR",
      "kind": "wasm",
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "exec_ref": null,
      "archived": false
    }
  ],
  "next_cursor": "eyJ2IjoxLCJpZCI6IkNBRVdUUVFXM1JHS1Y2N0lLMks2NEJOWFJGVVlUUE5JREtTVEdaVUxWM0UzSlFNUVFFQ1RCUkNSIn0",
  "parser_version": "1",
  "tier": "inferred",
  "ruleset_versions": {
    "41": "sep41-v0.5.2"
  }
}
```

```text
$ /workspace/scratch/stage-g/sep47idx query --db /workspace/scratch/stage-g/testnet.db --implements 41 --tier inferred --limit 1 --csv
contract_id,kind,wasm_hash,exec_ref_owner,exec_ref_tag,archived,tier
CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR,wasm,7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5,,,false,inferred
stderr:
next cursor: eyJ2IjoxLCJpZCI6IkNBRVdUUVFXM1JHS1Y2N0lLMks2NEJOWFJGVVlUUE5JREtTVEdaVUxWM0UzSlFNUVFFQ1RCUkNSIn0
```

## contract

Accepts flags before the ID or after it. `--history` returns claims for all
versions; the ordinary detail includes version ranges and current claims.
Unresolved references and never-run verification results remain null.
A verified pass means the suite's checks passed; it is not a security audit.
Missing contracts exit 2; malformed IDs exit 1.

```text
$ /workspace/scratch/stage-g/sep47idx contract CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR --db /workspace/scratch/stage-g/testnet.db --json
{
  "contract_id": "CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR",
  "network": "testnet",
  "kind": "wasm",
  "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
  "exec_ref": null,
  "archived": false,
  "claims": [
    {
      "sep": 41,
      "declared": false,
      "inferred": {
        "status": "match",
        "ruleset_version": "sep41-v0.5.2",
        "missing": [],
        "mismatched": []
      },
      "verified": null,
      "protocol": false,
      "source": "interface-match",
      "anomaly": null
    }
  ],
  "history": [
    {
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "from_ledger": 5067557,
      "to_ledger": null,
      "exec_ref": null
    }
  ],
  "parser_version": "1",
  "as_of_ledger": 5097816
}
```

```text
$ /workspace/scratch/stage-g/sep47idx contract CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR --db /workspace/scratch/stage-g/testnet.db --history --json
{
  "contract_id": "CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR",
  "network": "testnet",
  "history": [
    {
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "from_ledger": 5067557,
      "to_ledger": null,
      "exec_ref": null,
      "claims": [
        {
          "sep": 41,
          "declared": false,
          "inferred": {
            "status": "match",
            "ruleset_version": "sep41-v0.5.2",
            "missing": [],
            "mismatched": []
          },
          "verified": null,
          "protocol": false,
          "source": "interface-match",
          "anomaly": null
        }
      ]
    }
  ],
  "parser_version": "1",
  "as_of_ledger": 5097816,
  "next_cursor": null
}
```

## wasm

Shows parsed metadata, declaration tokens, anomalies and interface matches.
`--limit` and `--cursor` paginate current live contracts using the code. Missing
hashes exit 2; hashes that are not 64 lowercase hex characters exit 1.

```text
$ /workspace/scratch/stage-g/sep47idx wasm 7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5 --db /workspace/scratch/stage-g/testnet.db --json
{
  "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
  "meta": [
    {
      "key": "rsver",
      "value": "1.97.1"
    },
    {
      "key": "rssdkver",
      "value": "26.1.0#175aa41306f383057a8cdfc84b68d931664fc34e"
    },
    {
      "key": "cliver",
      "value": "27.0.0#5a7c5fe76530bf4248477ac812fc757146b98cc4"
    }
  ],
  "claims": [],
  "anomalies": [],
  "interface_matches": [
    {
      "sep": 41,
      "ruleset_version": "sep41-v0.5.2",
      "status": "match",
      "ok_count": 10,
      "missing": [],
      "mismatched": [],
      "matched_variants": {
        "transfer.inputs[1]": "Address"
      }
    }
  ],
  "contracts": [
    {
      "contract_id": "CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR",
      "kind": "wasm",
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CC6DOVVZ3XMBCNBWBOHELZNB2MJ5MLV4363DORZSDBQFH73CJD4VDWF2",
      "kind": "wasm",
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CCBMBHKOCWWHPR5WMOIFZJL3HJAIFFKHEXA4RMV5AZSTXDGR7IIAAKMF",
      "kind": "wasm",
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CCLDBPNWLEKX36LQ6G4M7IRXTOVTXXKIVASD34BP2ZWOJK5UM2NLEN3S",
      "kind": "wasm",
      "wasm_hash": "7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5",
      "exec_ref": null,
      "archived": false
    }
  ],
  "next_cursor": null,
  "parse_status": "ok",
  "parse_error": null,
  "parser_version": "1",
  "as_of_ledger": 5097816
}
```

## stats

Reads adoption, the inferred undeclared gap, anomaly tokens and the last indexed
ledger from one database snapshot. It requires no network request. Use API
health for fresh RPC tip and lag; a stored sync point does not imply freshness.

```text
$ /workspace/scratch/stage-g/sep47idx stats --db /workspace/scratch/stage-g/testnet.db
testnet: last indexed ledger 5097816; 200 stored contracts, 9 archived
Live: 185 Wasm, 0 Wasm references (0 unresolved), 6 SAC (protocol)
Declared: 0/183 measured contracts, 0/107 used hashes
Undeclared SEP-41 gap (inferred, sep41-v0.5.2): 27 contracts, 7 hashes
Anomalies: map[]
```

```text
$ /workspace/scratch/stage-g/sep47idx stats --db /workspace/scratch/stage-g/testnet.db --json
{
  "network": "testnet",
  "adoption": {
    "LiveSAC": 6,
    "LiveWasm": 185,
    "LiveWasmRef": 0,
    "WasmRefUnresolved": 0,
    "MeasuredContracts": 183,
    "UsedHashes": 107,
    "DeclaringContracts": 0,
    "DeclaringUsedHashes": 0,
    "SEP41ByContract": {
      "match": 27,
      "mismatch": 138,
      "partial": 18
    },
    "SEP41ByUsedHash": {
      "match": 7,
      "mismatch": 98,
      "partial": 2
    },
    "GapContracts": 27,
    "GapUsedHashes": 7
  },
  "anomalies": {},
  "archived_contracts": 9,
  "total_contracts": 200,
  "last_ledger": 5097816,
  "ruleset_versions": {
    "41": "sep41-v0.5.2"
  },
  "parser_version": "1"
}
```

## serve

Requires an existing index and opens it read-only. RPC network validation runs
at startup; mainnet needs `--rpc-url`. `--addr` defaults to `:8080` and
`--rate-limit` defaults to 0 (off). GET requests have deadlines and CORS.
SIGINT and SIGTERM shut down gracefully. The server does not sync the index.

Captured local start on 9 October 2026:

```text
$ /workspace/scratch/stage-g/sep47idx serve --network testnet --db /workspace/scratch/stage-g/testnet.db --addr 127.0.0.1:8080
serving testnet read-only on 127.0.0.1:8080
```

The [API reference](api.md) contains actual requests and responses from this
server. No public deployment is claimed.

## verify

Deferred by the recorded Phase 0 adoption decision. No verifier command or
transaction submission ships in this build.
