# CLI reference

The binary is `sep47idx`. Historical outputs below were captured on 7 October 2026. Command captures were refreshed from a Linux build on 9 October 2026 (Go 1.27.2). The `maintenance` captures come from a darwin/arm64 build on 20 November 2026 (Go 1.27.4). The help output and the `inspect` captures come from a darwin/arm64 build on 21 November 2026 (Go 1.27.4).

Every flag also has an environment variable: `SEP47IDX_` plus the flag name in capitals, dashes as underscores (`--rpc-url` is `SEP47IDX_RPC_URL`). A flag on the command line overrides the variable. Exit codes: `0` ok, `1` error, `2` not found.

```text
$ ./sep47idx help
usage: sep47idx <command> [flags]

commands:
  check        compare a seeded sample of stored contracts with live RPC entries
  contract     show a contract's claims and optional full history
  gap          list contracts that match a SEP's interface but declare nothing (inferred)
  inspect      analyze a local Wasm file offline, as the indexer would
  maintenance  check integrity, refresh query statistics and vacuum while no sync is writing
  phase0       measure SEP-47 adoption and write the adoption report
  query        query current contracts by SEP and trust tier
  serve        serve the index through the read-only JSON API
  stats        show stored adoption gap anomalies and sync progress
  sync         seed the index, sync ledgers or recompute matches
  version      print the binary version
  wasm         show parsed Wasm metadata and interface evidence
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

## Parser and XDR limits

`sync`, `phase0`, `inspect` and `check` are the commands that parse untrusted
Wasm or XDR, so they accept the five limits that bound it. Sizes take a plain
byte count or a suffix (`4MiB`, `512KiB`, `2MB`). Each has an environment
variable, and a flag overrides it. A zero, a negative number, an unparseable
size, or anything above a hard cap is refused at startup, before a database is
opened or a byte is read.

| Flag | Env var | Default | Hard cap |
| --- | --- | --- | --- |
| `--max-wasm-bytes` | `SEP47IDX_MAX_WASM_BYTES` | `1MiB` | `64MiB` |
| `--max-section-bytes` | `SEP47IDX_MAX_SECTION_BYTES` | `256KiB` | `--max-wasm-bytes` |
| `--max-sections` | `SEP47IDX_MAX_SECTIONS` | `10000` | `--max-wasm-bytes`/2 |
| `--max-xdr-depth` | `SEP47IDX_MAX_XDR_DEPTH` | `500` | `1500` |
| `--max-xdr-base64-bytes` | `SEP47IDX_MAX_XDR_BASE64_BYTES` | `64MiB` | `256MiB` |

Raising `--max-wasm-bytes` raises the base64 cap on the `CONTRACT_CODE` entry
with it, so one flag is enough to index a larger module. The limits a `sync`
ran with are recorded in `sync_state` and printed by `stats`. Each cap and the
memory trade-off are explained in
[Self-hosting](self-hosting.md#parser-and-xdr-limits).

```text
$ sep47idx sync --db testnet.db --max-wasm-bytes 65MiB --start-ledger 1
sep47idx sync: --max-wasm-bytes: invalid limit: MaxWasmBytes is 68157440, want at most 67108864 bytes, the hard cap
$ echo "exit=$?"
exit=1
```

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

If testnet retention advances during sampling, the command confirms the new
bounds with a fresh successful RPC probe and retries up to three times per
failed page. It preserves the original window, strata and scan limits.
Expired planned ledger ranges are recorded in `sample.retention_gaps`, logged
as warnings, and marked as incomplete coverage in the JSON method and adoption
report. An expired stratum can therefore contribute fewer contracts than its
quota. Other RPC failures and exhausted retries still fail the command.

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

Each invocation returns one bounded page: `--limit` is 1..500 (default 50),
and `--cursor` resumes after the last returned contract. The existing human
header and JSON evidence fields are retained; their count is the number of
contracts on this page. Human output writes `next cursor: ...` to stderr
when another page exists. JSON adds `next_cursor` only when there is a next
page, and retains `contracts: []` for an empty result. Pass the cursor back
with the same network and SEP; it is opaque, not a total-count or snapshot
identifier. Concurrent synchronization can change results between pages.
`SEP47IDX_LIMIT` and `SEP47IDX_CURSOR` are supported, with explicit flags
taking precedence. The read timeout covers the page query.

```sh
sep47idx gap --sep 41 --limit 100 --json
sep47idx gap --sep 41 --limit 100 --cursor '<next_cursor>' --json
```

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
`next_cursor`; human and default one-page CSV output write continuation information to stderr.
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

### Complete CSV export

Use `query --csv --all` to stream all remaining pages, with one header and at
most `--limit` contracts loaded per page. `--all` requires `--csv`; ordinary CSV,
JSON and human queries retain their one-page behavior. An optional `--cursor`
starts the export after that cursor, using the same filters and tier.

```sh
sep47idx query --db /path/to/testnet.db --implements 41 --tier inferred --csv --all --limit 500 > contracts.csv
```

Successful complete exports do not print a continuation cursor. Output and query
errors fail the command; pages are flushed before the next query. If a later
query fails, stderr identifies the cursor before the failed page so a resumed
export can continue with `--cursor`. Each invocation writes its own header;
omit the second header when combining successful pages. A write failure may
leave a partial page, so discard that incomplete output rather than assuming
it is a complete or safely resumable export.

Each page observes the current database independently: concurrent sync may
change matches between pages. This is not a single cross-page snapshot. Use a
checkpointed frozen database when a repeatable export is required. The existing
read-command timeout (`--rpc-timeout`, default 30s) covers the entire export;
raise it for a large database. No complete result is accumulated in memory.

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

## inspect

Runs the indexer's own analysis on a local Wasm file and prints the declaration,
anomalies and inferred interface verdict, with the missing and mismatched
functions. It opens no database and makes no RPC call, so it works offline,
before a contract is deployed. The file is parsed, never executed.

It calls the same code `sync` uses to analyze and store a Wasm, with the same
limits (by default 1 MiB per module, 256 KiB per section, 10,000 sections; see
[Parser and XDR limits](#parser-and-xdr-limits)). A file over the module limit
exits 1 before it is read in full, and `--max-wasm-bytes` raises that limit for
this command as it does for `sync`.
Malformed Wasm is not an error: it is analyzed and its `parse_status` and
`parse_error` are printed, as the indexer would store them.

The output reads like `sep47idx wasm` for the same code after indexing, minus
the live contracts and the indexed ledger, which a local file does not have.
`--json` prints the `wasm --json` fields that come from the code: `wasm_hash`,
`meta`, `claims`, `anomalies`, `interface_matches`, `parse_status`,
`parse_error` and `parser_version`. `TestInspectOutputMatchesIndexedWasm`
indexes every golden fixture through the ledger path and requires both outputs
to agree.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--json` | off | JSON output. |
| `--require-match` | none | Exit 1 unless the inferred status for this SEP is `match`. A SEP with no rule file is an error. |
| `--partial-threshold` | `0.5` | `match.partial_threshold`, as for `sync`. |

Exit codes: `0` analyzed, `1` error or a failed `--require-match`.

Captured on 21 November 2026 from the committed fixtures in `testdata/wasm`:

```text
$ sep47idx inspect token_partial.wasm
2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880: ok (parser 1), local file token_partial.wasm, not indexed
Metadata: [{"key":"sep","value":"41"},{"key":"rsver","value":"1.98.1"},{"key":"rssdkver","value":"28.0.0#48d506712f964094d14176e2f0b02afcd1054567"},{"key":"rssdk_spec_shaking","value":"2"},{"key":"cliver","value":"28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"}]
Declared SEP-41 (sep47-meta), token="41", anomaly=none
Inferred SEP-41: partial (sep41-v0.5.2), 8 functions matched
  Missing: burn, burn_from
$ echo $?
0
$ sep47idx inspect token_partial.wasm --require-match 41
...
  Missing: burn, burn_from
sep47idx inspect: required interface does not match: SEP-41 is partial
$ echo $?
1
$ sep47idx inspect token_full_sep.wasm --require-match 41
1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96: ok (parser 1), local file token_full_sep.wasm, not indexed
Metadata: [{"key":"sep","value":"41"},{"key":"rsver","value":"1.98.1"},{"key":"rssdkver","value":"28.0.0#48d506712f964094d14176e2f0b02afcd1054567"},{"key":"rssdk_spec_shaking","value":"2"},{"key":"cliver","value":"28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"}]
Declared SEP-41 (sep47-meta), token="41", anomaly=none
Inferred SEP-41: match (sep41-v0.5.2), 10 functions matched
$ echo $?
0
```

## wasm

Shows parsed metadata, declaration tokens, anomalies and interface matches.
`--limit` and `--cursor` paginate current live contracts using the code. Missing
hashes exit 2; hashes that are not 64 lowercase hex characters exit 1.

Text output lists what kept an inferred match from being complete. Under each
`Inferred SEP-N` line it prints `Missing: f, g` for absent functions and
`Mismatched f: expected (A, B) -> C, found (A) -> C` for a function with the
wrong types. A function defined twice prints `defined more than once`. For an
input that accepts several types, such as `transfer.to`, "expected" shows the
first accepted type. `--json` carries the same evidence in `missing` and
`mismatched`. `TestMatchEvidenceFromRealMatcher` checks this output against the
real matcher on the `token_partial.wasm` fixture, which prints
`Missing: burn, burn_from`. A declaration line prints `anomaly=none` or the
anomaly code.

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
ledger from one database snapshot. By default it makes no network request, and
a stored sync point does not imply freshness.

`--lag` adds a fresh sync check. It asks the configured RPC (`--rpc-url` or
`SEP47IDX_RPC_URL`; testnet has a default) for its network and latest ledger.
It refuses a provider on another network. It reports `latest_ledger` and
`lag` (latest minus last indexed) in JSON, and a final
`Sync: RPC latest ledger N, lag L ledgers` line in text. A provider behind the
index is an error, not a negative lag. Without `--lag`, the JSON has no `lag`
field, so a missing value is never mistaken for lag 0.
`TestStatsCLILag` in `cmd/sep47idx/cmd_stats_test.go` covers each case.

A `Limits (recorded by the last sync)` line reports the parser and XDR limits
the last `sync` ran with, read from `sync_state`, with the same values under a
`limits` key in JSON. An index synced before those limits were recorded prints
`Limits: not recorded` rather than the binary's current defaults, because
nothing in that database says what its data was parsed with. The HTTP
`GET /v1/stats` body is a fixed surface and does not carry them.

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

To serve both networks from one process, pass comma-separated
`network=value` maps for `--db` and `--rpc-url`:

- `--db mainnet=./data/mainnet.db,testnet=./data/testnet.db`
- `--rpc-url mainnet=<mainnet-url>,testnet=<testnet-url>`

The maps must name the same networks, contain at least two entries, and values
must not contain commas. Each database is opened read-only and each RPC URL is
checked against its network passphrase at startup. Do not combine this form
with `--network`. API requests then require `network=mainnet|testnet`; see the
[API reference](api.md).

The [API reference](api.md) contains actual requests and responses from this
server. No public deployment is claimed.

## check

Compares a reproducible sample of stored contracts with live RPC entries. It
opens the database read-only and never writes. Use it after a sync to catch
index drift: a contract whose kind, Wasm hash, CAP-85 reference or SAC asset no
longer matches the network.

```sh
sep47idx check --network testnet --db ./data/testnet.db --sample 100 --seed 1
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--sample` | `100` | Contracts to compare, 1..100000. |
| `--seed` | `1` | Sampling seed. The same database and seed always pick the same contracts. |
| `--json` | off | Print the full result as JSON. |

Entries whose `lastModifiedLedgerSeq` is after the index's `last_ledger` are
counted as `changed_after_index` and skipped, because the index cannot know
about a change it has not synced yet. Entries the index already records as
archived are counted, not reported. Any other missing or differing field is a
difference. Exit 0 means no differences; exit 1 means at least one difference
or an error; exit 2 means the database has no `last_ledger`.

## maintenance

Keeps a long-running index healthy without hand-written SQL. It takes the
database write lock (`BEGIN IMMEDIATE`), then runs `PRAGMA integrity_check`,
`ANALYZE` and, when the file's `auto_vacuum` mode is incremental,
`PRAGMA incremental_vacuum`, commits, and checkpoints the WAL. It reports each
step's result and duration, the size before and after, and the total time.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--enable-incremental-vacuum` | off | Switch `auto_vacuum` to `INCREMENTAL`. SQLite applies this only through one full `VACUUM`, which rewrites the file and needs free disk about the size of the database. Later runs then vacuum incrementally. |
| `--timeout` | `1h` | Give up after this long. An unfinished run rolls back. |

Rules:

- **It refuses, without waiting, if another process holds the write lock**,
  which is what a sync does while it writes a batch. Stop `sync --follow` first.
  The lock check catches a batch in flight, but not a follow process that is
  idle between batches; if maintenance runs then, that sync's next batch waits
  up to its 10 s busy timeout and fails if maintenance takes longer.
- If the integrity check finds problems, it prints them, exits 1, and changes
  nothing else: no `ANALYZE`, no vacuum. Keep the file for diagnosis and
  restore from a backup.
- It never creates or migrates a database. It refuses a missing file, another
  network, or a schema that is older or newer than the binary.
- The read-only API can keep serving during maintenance.

Exit codes: `0` when every step succeeded, `1` otherwise (lock held, integrity
problems, bad arguments).

Captured on 20 November 2026 with a local darwin/arm64 build (Go 1.27.4) against
a testnet index synced from ledger 5,829,500 to 5,830,967 a minute earlier:

```text
$ ./sep47idx stats --db testnet.db
testnet: last indexed ledger 5830967; 311 stored contracts, 0 archived
Live: 232 Wasm, 16 Wasm references (0 unresolved), 63 SAC (protocol)
Declared: 1/248 measured contracts, 1/98 used hashes
Undeclared SEP-41 gap (inferred, sep41-v0.5.2): 40 contracts, 5 hashes
Anomalies: map[]

$ ./sep47idx maintenance --db testnet.db
database: testnet.db
lock:                      write lock held (0 ms)
integrity_check:           ok (1 ms)
analyze:                   query statistics refreshed (0 ms)
commit:                    write lock released (0 ms)
incremental_vacuum:        skipped: auto_vacuum is none, so SQLite cannot vacuum incrementally (0 free page(s)); run once with --enable-incremental-vacuum, which does a full VACUUM (0 ms)
wal_checkpoint:            WAL checkpointed and truncated (0 ms)
size before:               569344 bytes (139 pages of 4096, 0 free); on disk 569344 bytes
size after:                602112 bytes (147 pages of 4096, 0 free); on disk 602112 bytes
duration:                  3 ms

$ ./sep47idx maintenance --db testnet.db --enable-incremental-vacuum
database: testnet.db
lock:                      write lock held (0 ms)
integrity_check:           ok (1 ms)
analyze:                   query statistics refreshed (0 ms)
commit:                    write lock released (0 ms)
enable_incremental_vacuum: full VACUUM done; auto_vacuum is now incremental (3 ms)
wal_checkpoint:            WAL checkpointed and truncated (0 ms)
size before:               602112 bytes (147 pages of 4096, 0 free); on disk 602112 bytes
size after:                598016 bytes (146 pages of 4096, 0 free); on disk 598016 bytes
duration:                  7 ms
```

The first run grew the file by eight pages: `ANALYZE` created the
`sqlite_stat1` statistics table. Once `auto_vacuum` is incremental, the vacuum
runs inside the locked transaction (`--json` output, trimmed):

```text
$ ./sep47idx maintenance --db testnet.db --json
{
  "database": "testnet.db",
  "integrity_ok": true,
  "integrity": [
    "ok"
  ],
  "auto_vacuum": "incremental",
  "freed_pages": 0,
  "steps": [
...
    {
      "name": "incremental_vacuum",
      "result": "freed 0 page(s)",
      "duration_ms": 0
    },
...
  ],
...
  "duration_ms": 3
}
```

With a second connection holding `BEGIN IMMEDIATE` (opened with the `sqlite3`
shell to stand in for a sync batch):

```text
$ ./sep47idx maintenance --db testnet.db; echo "exit $?"
database: testnet.db
lock:                      error: another process holds the database write lock (stop sync before running maintenance): database is locked (5) (SQLITE_BUSY) (0 ms)
size before:               598016 bytes (146 pages of 4096, 0 free); on disk 598016 bytes
size after:                unchanged (stopped before any change)
duration:                  1 ms
sep47idx maintenance: another process holds the database write lock (stop sync before running maintenance): database is locked (5) (SQLITE_BUSY)
exit 1
```

The corrupted-database path is covered by `TestMaintainReportsCorruptedCopy` in
`internal/store/maintenance_test.go`: it flips one byte of a stored hash inside
an index page, and maintenance reports `row … missing from index …`, exits with
`ErrIntegrity`, and leaves the file byte-for-byte unchanged.

## verify

Deferred by the recorded Phase 0 adoption decision. No verifier command or
transaction submission ships in this build.
