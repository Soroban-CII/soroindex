# HTTP API

The server implements all `/v1` endpoints and opens the index read-only. Start it
with `sep47idx serve`. It serves JSON and enables GET CORS. POST and other write
methods return a JSON error. `--rate-limit` sets requests per minute per peer IP;
0 disables it. Forwarding headers do not override the peer address.

Contract IDs must be full `C...` strkeys. Wasm hashes must be 64 lowercase hex
characters. Invalid input returns 400; absent resources return 404. Unknown
paths return 404. Collections return 200 with an empty array when nothing matches.
Errors use `{"error":{"code":"not_found|bad_request|rate_limited|internal","message":"..."}}`.

## Queries and pagination

| Endpoint | Parameters and result |
| --- | --- |
| `/v1/contracts` | `implements` is a comma list with AND; `implements_any` is OR. Both together require the AND list and at least one OR entry. `tier` is declared (default), inferred, verified or protocol; `kind` is wasm, wasm_ref or sac. `/contracts` is an alias. |
| `/v1/contracts/{id}` | Fixed contract detail, all version ranges, claims, archival and executable reference. Takes no parameters. |
| `/v1/contracts/{id}/history` | Claims for each version. Accepts `limit` and `cursor`. |
| `/v1/wasm/{hash}` | Metadata, declarations, anomalies and matches; current live users accept `limit` and `cursor`. |
| `/v1/seps` | Distinct live contract counts per SEP and tier; accepts `limit` and `cursor`. |
| `/v1/stats` | Stored adoption, undeclared gap, anomaly token counts, archival and last indexed ledger. Takes no parameters. |
| `/v1/health` | Network, stored last ledger, fresh RPC tip and lag. Takes no parameters. |

Pages default to 50 rows, at most 500. Pass `next_cursor` back unchanged with the
same filters; null means the end. Cursors are opaque base64url values. History
cursors are scoped to the contract. Pagination observes current data on each
request; it does not hold a snapshot across separate requests.

Inferred results use loaded rule versions. SACs belong to the protocol tier and
never satisfy declared or inferred filters. Verified filtering requires a current
hash, a declaration and the latest passing result. Old-code results remain in
history with `stale: true`. Never-run results are null. A pass means the suite's
checks passed; it is not a security audit. No verifier execution ships in this build.

The `wasm_ref` detail carries `{owner, tag}`. Unresolved references have a null
Wasm hash and no fabricated claims. Archival preserves details and history while
excluding the contract from live lists and counts.

## Real testnet captures

Captured on 9 October 2026 from a real 200-contract retention-window sample,
seeded into `/workspace/scratch/stage-g/testnet.db`. This sample is not the full
network census. It has no declarations, so the declared query is honestly empty.
The seed is static: health lag grows until sync runs again. `status: ok` means
health could read the index and RPC tip; it does not assert a lag target.

The requests below were executed with curl against the local validation server.
This is a command reference, not a hosted deployment. JSON is formatted for reading.

### Declared contracts

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/contracts?implements=41'
```

```json
{
  "contracts": [],
  "next_cursor": null,
  "parser_version": "1",
  "tier": "declared"
}
```

### Inferred contracts

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/contracts?implements=41&tier=inferred&limit=1'
```

```json
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

### Protocol contracts through the alias

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/contracts?tier=protocol&limit=1'
```

```json
{
  "contracts": [
    {
      "contract_id": "CASK4VAA6RFSYXGD6HHQSKD3UBKRFHZL5DDWM3VXTCVTI27PRWTAFNQB",
      "kind": "sac",
      "wasm_hash": null,
      "exec_ref": null,
      "archived": false
    }
  ],
  "next_cursor": "eyJ2IjoxLCJpZCI6IkNBU0s0VkFBNlJGU1lYR0Q2SEhRU0tEM1VCS1JGSFpMNUREV00zVlhUQ1ZUSTI3UFJXVEFGTlFCIn0",
  "parser_version": "1",
  "tier": "protocol"
}
```

### Contract detail

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/contracts/CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR'
```

```json
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

### Version history

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/contracts/CAEWTQQW3RGKV67IK2K64BNXRFUYTPNIDKSTGZULV3E3JQMQQECTBRCR/history'
```

```json
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

### Wasm evidence

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/wasm/7494c2bb5e9f6dd3f831b078400746b86f748a65cf14f593b1adb3572e2feea5'
```

```json
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

### SEP counts

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/seps'
```

```json
{
  "seps": [
    {
      "sep": 41,
      "declared": 0,
      "inferred": 27,
      "verified": 0,
      "protocol": 6
    }
  ],
  "next_cursor": null,
  "ruleset_versions": {
    "41": "sep41-v0.5.2"
  },
  "parser_version": "1",
  "as_of_ledger": 5097816
}
```

### Stored statistics

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/stats'
```

```json
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

### Live health

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/health'
```

```json
{
  "status": "ok",
  "network": "testnet",
  "last_ledger": 5097816,
  "latest_ledger": 5097914,
  "lag": 98
}
```

### Continue an inferred page

```sh
curl --fail --silent --show-error 'http://127.0.0.1:8080/v1/contracts?implements=41&tier=inferred&limit=1&cursor=eyJ2IjoxLCJpZCI6IkNBRVdUUVFXM1JHS1Y2N0lLMks2NEJOWFJGVVlUUE5JREtTVEdaVUxWM0UzSlFNUVFFQ1RCUkNSIn0'
```

```json
{
  "contracts": [
    {
      "contract_id": "CAEXK2QQJYJLQPWZKWEPJMHL73VD5PPLJVKC6WMJL3W5YMDQ2EKNMJZJ",
      "kind": "wasm",
      "wasm_hash": "a5f6b06ca8fb10a7f3d12ba5fae3361f04a242054757080cf14c375b88e1c898",
      "exec_ref": null,
      "archived": false
    }
  ],
  "next_cursor": "eyJ2IjoxLCJpZCI6IkNBRVhLMlFRSllKTFFQV1pLV0VQSk1ITDczVkQ1UFBMSlZLQzZXTUpMM1c1WU1EUTJFS05NSlpKIn0",
  "parser_version": "1",
  "tier": "inferred",
  "ruleset_versions": {
    "41": "sep41-v0.5.2"
  }
}
```

See the [million-claim benchmark](query-benchmark.md) and [dependency review](dependency-review.md) for measured query latency and security checks.
