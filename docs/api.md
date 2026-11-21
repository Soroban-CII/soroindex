# HTTP API

> Machine-readable spec: [docs/openapi.yaml](openapi.yaml) (OpenAPI 3.1.0)

The server implements all `/v1` endpoints and opens the index read-only. Start it
with `sep47idx serve`. It serves JSON and enables GET CORS. POST and other write
methods return a JSON error. `--rate-limit` sets requests per minute per peer IP;
0 disables it. Forwarding headers do not override the peer address.

Contract IDs must be full `C...` strkeys. Wasm hashes must be 64 lowercase hex
characters. Invalid input returns 400; absent resources return 404. Unknown
paths return 404. Collections return 200 with an empty array when nothing matches.
Errors use `{"error":{"code":"not_found|bad_request|rate_limited|internal","message":"..."}}`.

A server started with one `--db` serves that network. If it is started with
`--db mainnet=<path>,testnet=<path>`, every endpoint requires
`network=mainnet` or `network=testnet`; omitting it, repeating it or naming an
unknown network returns 400. A single-network server also accepts `network`
with its own name. The parameter is validated before the endpoint runs and is
not passed to handlers that otherwise reject query parameters.

## Queries and pagination

| Endpoint | Parameters and result |
| --- | --- |
| `/v1/contracts` | `implements` is a comma list with AND; `implements_any` is OR. Both together require the AND list and at least one OR entry. `tier` is declared (default), inferred, verified or protocol; `kind` is wasm, wasm_ref or sac. `/contracts` is an alias. `ids` switches to a bulk lookup; see below. |
| `/v1/contracts/{id}` | Fixed contract detail, all version ranges, claims, archival and executable reference. Takes only `network`. |
| `/v1/contracts/{id}/history` | Claims for each version. Accepts `limit` and `cursor`. |
| `/v1/wasm/{hash}` | Metadata, declarations, anomalies and matches; current live users accept `limit` and `cursor`. |
| `/v1/seps` | Distinct live contract counts per SEP and tier; accepts `limit` and `cursor`. |
| `/v1/stats` | Stored adoption, undeclared gap, anomaly token counts, archival and last indexed ledger. Takes only `network`. |
| `/v1/stats/toolchains` | Rust, soroban-sdk and Stellar CLI versions from stored meta, with declaration and SEP-41 match rates per version. Takes only `network`. |
| `/v1/health` | Network, stored last ledger, fresh RPC tip and lag. Takes only `network`. |

Pages default to 50 rows, at most 500. Pass `next_cursor` back unchanged with the
same filters; null means the end. Cursors are opaque base64url values. History
cursors are scoped to the contract. Pagination observes current data on each
request; it does not hold a snapshot across separate requests.

Inferred results use loaded rule versions. SACs belong to the protocol tier and
never satisfy declared or inferred filters. Verified filtering requires a current
hash, a declaration and the latest passing result. Old-code results remain in
history with `stale: true`. Never-run results are null. A pass means the suite's
checks passed; it is not a security audit. No verifier execution ships in this build.

## Bulk lookup

`GET /v1/contracts?ids=C...,C...` returns the summaries of up to 100 contracts
in one request, for clients such as wallets that check every token a user holds.

- Every entry is validated as a full `C...` contract strkey before any query.
  One invalid entry fails the whole request with 400, and the message gives its
  1-based position, not the input. Duplicates collapse to their first position.
- More than 100 entries returns 400. Exactly 100 is accepted.
- `ids` cannot be combined with `implements`, `implements_any`, `tier`, `kind`,
  `limit` or `cursor`. Mixing them would leave it unclear whether an absent ID is
  unknown or only filtered out.
- `contracts` holds the found contracts in request order, in the same summary
  shape as a `/v1/contracts` page. Archived contracts are included with
  `archived: true`.
- `not_found` lists every requested ID the index does not know. No ID is
  dropped silently. The response also carries `parser_version` and
  `as_of_ledger`.
- Rate limiting charges one request per started block of 10 IDs: a 1-10 ID
  lookup costs 1 and a 100-ID lookup costs 10. A lookup that costs more than
  the whole `--rate-limit` budget is charged the whole budget, so it can still
  run once per minute.

The lookup reports what the index stores. It does not filter by tier. Use the
contract detail endpoint, or a filtered list, for claims.

## Toolchain statistics

`GET /v1/stats/toolchains` buckets the code the index measures by the `rsver`,
`rssdkver` and `cliver` meta values that the Soroban toolchain writes. Measured
code is the same population as `adoption` in `/v1/stats`: live `wasm` and
resolved `wasm_ref` contracts whose current Wasm was parsed. Counts come from
stored `meta_json`.

- Each bucket counts unique Wasm hashes and live contracts, side by side,
  because factory deployments inflate per-contract numbers.
- `declaring_*` counts code that declares any SEP. `sep41_match_*` counts code
  whose inferred SEP-41 status is `match` under the loaded rule file, named in
  `sep41_ruleset_version` (null when no SEP-41 rule file is loaded).
- `*_pct_*` values are percentages of the bucket's own hashes or contracts,
  rounded to two decimals.
- `unknown` covers a missing key, an empty value, a value longer than 64 bytes,
  or one with control characters or invalid UTF-8. `multiple` covers code whose
  meta gives the key more than one distinct value. The index does not pick one.
- Values are reported as written, including any `#commit` suffix.

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

## Fixture snapshots

Every endpoint has a golden response snapshot in
`internal/api/testdata/snapshots/`. The test builds a fixture index through the
real ingest path. It applies the two recorded testnet ledgers 5062907 and
5062908, then two synthetic ledgers that create the golden Wasm fixtures, a
Stellar Asset Contract, a CAP-85 reference and an unresolved reference, and
upgrade one contract from `token_full_sep.wasm` to `token_partial.wasm`. The
RPC stub holds no entries, so code that only the recorded ledgers reference is
archived. Timestamps are replaced with `"<rfc3339>"`. `go test ./internal/api`
fails when a response drifts from its snapshot. Run
`go test ./internal/api -run Snapshot -update` to accept a reviewed change.

The examples below are generated from those snapshots, and the same test fails
if they differ. They show fixture data, not the live network.

<!-- BEGIN fixture snapshots: generated by `go test ./internal/api -run Snapshot -update`; do not edit by hand -->

### Contracts declaring SEP-41

```text
GET /v1/contracts?implements=41
```

```json
{
  "contracts": [
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU",
      "kind": "wasm",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGXCU",
      "kind": "wasm",
      "wasm_hash": "c51a97e72bb1d51aeebd9c60bfb174fb053a3a76292b9a779707713049eb000c",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAJOZE",
      "kind": "wasm",
      "wasm_hash": "82e05acf3bcb83adbc7ca8841fae5f7f412fd0cab61aa84015f94da9dc3fb54d",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU",
      "kind": "wasm",
      "wasm_hash": "2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU",
      "kind": "wasm_ref",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": {
        "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
        "tag": "token-v1"
      },
      "archived": false
    }
  ],
  "next_cursor": null,
  "parser_version": "1",
  "tier": "declared"
}
```

### Contracts whose interface matches SEP-41

```text
GET /v1/contracts?implements=41&tier=inferred
```

```json
{
  "contracts": [
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU",
      "kind": "wasm",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE7KE",
      "kind": "wasm",
      "wasm_hash": "b9dee7a76d341dcbc318157673752274f908e290e41735ba8c1305a522e5bcc5",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU",
      "kind": "wasm_ref",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": {
        "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
        "tag": "token-v1"
      },
      "archived": false
    }
  ],
  "next_cursor": null,
  "parser_version": "1",
  "tier": "inferred",
  "ruleset_versions": {
    "41": "sep41-v0.5.2"
  }
}
```

### Bulk lookup

```text
GET /v1/contracts?ids=CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU,CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAN6IE,CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU,CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQN7F,CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU,CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGH6RY
```

```json
{
  "contracts": [
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU",
      "kind": "wasm",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAN6IE",
      "kind": "sac",
      "wasm_hash": null,
      "exec_ref": null,
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU",
      "kind": "wasm_ref",
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "exec_ref": {
        "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
        "tag": "token-v1"
      },
      "archived": false
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQN7F",
      "kind": "wasm_ref",
      "wasm_hash": null,
      "exec_ref": {
        "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
        "tag": "never-defined"
      },
      "archived": true
    },
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU",
      "kind": "wasm",
      "wasm_hash": "2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880",
      "exec_ref": null,
      "archived": false
    }
  ],
  "not_found": [
    "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGH6RY"
  ],
  "parser_version": "1",
  "as_of_ledger": 5062910
}
```

### Declared and verified token

```text
GET /v1/contracts/CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU
```

```json
{
  "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACHTU",
  "network": "testnet",
  "kind": "wasm",
  "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
  "exec_ref": null,
  "archived": false,
  "claims": [
    {
      "sep": 41,
      "declared": true,
      "inferred": {
        "status": "match",
        "ruleset_version": "sep41-v0.5.2",
        "missing": [],
        "mismatched": []
      },
      "verified": {
        "verdict": "pass",
        "tool": "soroban-guard",
        "tool_version": "0.0.0-fixture",
        "run_at": "<rfc3339>",
        "stale": false
      },
      "protocol": false,
      "source": "sep47-meta",
      "anomaly": null
    }
  ],
  "history": [
    {
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "from_ledger": 5062909,
      "to_ledger": null,
      "exec_ref": null
    }
  ],
  "parser_version": "1",
  "as_of_ledger": 5062910
}
```

### CAP-85 reference contract

```text
GET /v1/contracts/CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU
```

```json
{
  "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPWAU",
  "network": "testnet",
  "kind": "wasm_ref",
  "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
  "exec_ref": {
    "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
    "tag": "token-v1"
  },
  "archived": false,
  "claims": [
    {
      "sep": 41,
      "declared": true,
      "inferred": {
        "status": "match",
        "ruleset_version": "sep41-v0.5.2",
        "missing": [],
        "mismatched": []
      },
      "verified": null,
      "protocol": false,
      "source": "sep47-meta",
      "anomaly": null
    }
  ],
  "history": [
    {
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "from_ledger": 5062909,
      "to_ledger": null,
      "exec_ref": {
        "owner": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAASFXV",
        "tag": "token-v1"
      }
    }
  ],
  "parser_version": "1",
  "as_of_ledger": 5062910
}
```

### Upgraded contract history

```text
GET /v1/contracts/CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU/history
```

```json
{
  "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU",
  "network": "testnet",
  "history": [
    {
      "wasm_hash": "1645f559c74fd1adc0fb0b27ed2005e1b272cdfc3a40b55d05e696417c0cae96",
      "from_ledger": 5062909,
      "to_ledger": 5062910,
      "exec_ref": null,
      "claims": [
        {
          "sep": 41,
          "declared": true,
          "inferred": {
            "status": "match",
            "ruleset_version": "sep41-v0.5.2",
            "missing": [],
            "mismatched": []
          },
          "verified": {
            "verdict": "pass",
            "tool": "soroban-guard",
            "tool_version": "0.0.0-fixture",
            "run_at": "<rfc3339>",
            "stale": true
          },
          "protocol": false,
          "source": "sep47-meta",
          "anomaly": null
        }
      ]
    },
    {
      "wasm_hash": "2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880",
      "from_ledger": 5062910,
      "to_ledger": null,
      "exec_ref": null,
      "claims": [
        {
          "sep": 41,
          "declared": true,
          "inferred": {
            "status": "partial",
            "ruleset_version": "sep41-v0.5.2",
            "missing": [
              "burn",
              "burn_from"
            ],
            "mismatched": []
          },
          "verified": null,
          "protocol": false,
          "source": "sep47-meta",
          "anomaly": null
        }
      ]
    }
  ],
  "parser_version": "1",
  "as_of_ledger": 5062910,
  "next_cursor": null
}
```

### Wasm evidence for a partial token

```text
GET /v1/wasm/2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880
```

```json
{
  "wasm_hash": "2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880",
  "meta": [
    {
      "key": "sep",
      "value": "41"
    },
    {
      "key": "rsver",
      "value": "1.98.1"
    },
    {
      "key": "rssdkver",
      "value": "28.0.0#48d506712f964094d14176e2f0b02afcd1054567"
    },
    {
      "key": "rssdk_spec_shaking",
      "value": "2"
    },
    {
      "key": "cliver",
      "value": "28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"
    }
  ],
  "claims": [
    {
      "sep": 41,
      "source": "sep47-meta",
      "raw_token": "41",
      "anomaly": null
    }
  ],
  "anomalies": [],
  "interface_matches": [
    {
      "sep": 41,
      "ruleset_version": "sep41-v0.5.2",
      "status": "partial",
      "ok_count": 8,
      "missing": [
        "burn",
        "burn_from"
      ],
      "mismatched": [],
      "matched_variants": {
        "transfer.inputs[1]": "MuxedAddress"
      }
    }
  ],
  "contracts": [
    {
      "contract_id": "CDYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALGRU",
      "kind": "wasm",
      "wasm_hash": "2cf19ac29ba8a9f53eda71c1b15a322d7704db28ed8d98d9e77ea8ce22fa5880",
      "exec_ref": null,
      "archived": false
    }
  ],
  "next_cursor": null,
  "parse_status": "ok",
  "parse_error": null,
  "parser_version": "1",
  "as_of_ledger": 5062910
}
```

### Toolchain statistics

```text
GET /v1/stats/toolchains
```

```json
{
  "network": "testnet",
  "measured_hashes": 5,
  "measured_contracts": 6,
  "toolchains": {
    "cliver": [
      {
        "version": "28.0.0#300aaf69ab100536678bdb641428b06f06b318ea",
        "hashes": 5,
        "contracts": 6,
        "declaring_hashes": 4,
        "declaring_contracts": 5,
        "sep41_match_hashes": 2,
        "sep41_match_contracts": 3,
        "declaring_pct_by_hash": 80,
        "declaring_pct_by_contract": 83.33,
        "sep41_match_pct_by_hash": 40,
        "sep41_match_pct_by_contract": 50
      }
    ],
    "rssdkver": [
      {
        "version": "28.0.0#48d506712f964094d14176e2f0b02afcd1054567",
        "hashes": 5,
        "contracts": 6,
        "declaring_hashes": 4,
        "declaring_contracts": 5,
        "sep41_match_hashes": 2,
        "sep41_match_contracts": 3,
        "declaring_pct_by_hash": 80,
        "declaring_pct_by_contract": 83.33,
        "sep41_match_pct_by_hash": 40,
        "sep41_match_pct_by_contract": 50
      }
    ],
    "rsver": [
      {
        "version": "1.98.1",
        "hashes": 5,
        "contracts": 6,
        "declaring_hashes": 4,
        "declaring_contracts": 5,
        "sep41_match_hashes": 2,
        "sep41_match_contracts": 3,
        "declaring_pct_by_hash": 80,
        "declaring_pct_by_contract": 83.33,
        "sep41_match_pct_by_hash": 40,
        "sep41_match_pct_by_contract": 50
      }
    ]
  },
  "sep41_ruleset_version": "sep41-v0.5.2",
  "parser_version": "1",
  "as_of_ledger": 5062910
}
```

<!-- END fixture snapshots -->

See the [million-claim benchmark](query-benchmark.md) and [dependency review](dependency-review.md) for measured query latency and security checks.
