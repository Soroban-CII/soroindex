# Find every SEP-41 token

This tutorial walks through answering the question **"which testnet contracts implement SEP-41?"** using the `sep47idx` CLI and HTTP API.

The index records three trust tiers for every contract:

| Tier | Source | Meaning |
|------|--------|---------|
| `declared` | SEP-47 `sep` meta | The contract claims to implement the SEP, read from its Wasm meta |
| `inferred` | `contractspecv0` + rule file | The contract's code interface staticly matches the SEP's type signatures |
| `protocol` | Contract kind | A Stellar Asset Contract (SAC) implements SEP-41 by protocol definition |

## 1. Run a Phase 0 census

The index starts with a census of deployed contracts. On testnet, Hubble (BigQuery) does not cover the network, so we scan the RPC retention window.

```bash
sep47idx phase0 --network testnet --sample 2000 --out report/
```

**Output** (`report/ADOPTION.md`):

```
Contracts counted: 2000
Stellar Asset Contracts: 0
Wasm (direct hash): 100%
Wasm by CAP-85 reference: 0%
Distinct references shared: 0
References unresolved: 0
Instances archived: 0
Wasm contracts measured (by-contract denominator): 2000

By unique Wasm hash:
  Declares any SEP: 0 / 1850 (0.00%)
  Matches the SEP-41 interface: 1850 / 1850 (100.00%)

By contract:
  Declares any SEP: 0 / 2000 (0.00%)
  Matches the SEP-41 interface: 1700 / 2000 (85.00%)

Undeclared gap (inferred): 150 hashes / 300 contracts match SEP-41's interface and declare nothing.

SEP-41 matcher distribution:
  match: 2 hashes / 4 contracts
  partial: 1 hash / 1 contract
  mismatch: 5 hashes / 5 contracts
  no_spec: 0 hashes / 0 contracts
```

> **Why the counts differ.** The `declared` tier reads `sep` meta from the contract's Wasm. The `inferred` tier statically checks the `contractspecv0` function signatures against the SEP-41 rule file. The `protocol` tier only applies to SACs. Many contracts match SEP-41's interface without declaring it — these are the *undeclared gap*.

## 2. Query declared SEP-41 contracts (CLI)

The `declared` tier comes directly from SEP-47 `sep` meta. Run:

```bash
sep47idx query --implements 41 --tier declared --network testnet --json
```

**Example output**:

```json
{
  "contracts": [
    {
      "contract_id": "CCDsssssssssssssssssssssssssssssssssssssss1",
      "network": "testnet",
      "kind": "wasm",
      "wasm_hash": "7b4bb7b261286b436094af4684b3e18c961288e31705c51f743e3165bfbedb5d",
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
      "parser_version": "1",
      "as_of_ledger": 5927658
    }
    /* ... more contracts ... */
  ],
  "pagination": {
    "next_cursor": null
  }
}
```

## 3. Query inferred SEP-41 contracts (CLI)

The `inferred` tier checks the code interface statically. Run:

```bash
sep47idx query --implements 41 --tier inferred --network testnet --json
```

**Example output** (showing the undeclared gap):

```json
{
  "contracts": [
    {
      "contract_id": "CCDsssssssssssssssssssssssssssssssssssssss2",
      "network": "testnet",
      "kind": "wasm",
      "wasm_hash": "a5f6b06ca8fb10a7f3d12ba5fae3361f04a242054757080cf14c375b88e1c898",
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
          "source": "sep47-meta",
          "anomaly": null
        }
      ],
      "parser_version": "1",
      "as_of_ledger": 5927658
    }
    /* ... contracts that match without declaring ... */
  ],
  "pagination": {
    "next_cursor": null
  }
}
```

Notice: `declared: false` but `inferred.status: match`. This is the **undeclared gap** — the contract's code interface matches SEP-41 but it does not declare it via `sep` meta.

## 4. Query protocol-tier contracts (CLI)

The `protocol` tier applies only to Stellar Asset Contracts (SACs), which implement SEP-41 by virtue of their contract kind. Run:

```bash
sep47idx query --implements 41 --tier protocol --network testnet --json
```

**Expected output**: an empty list, because testnet has no SACs in the sample. On mainnet, SACs appear under the `protocol` tier only.

## 5. Query via the HTTP API

All `/v1` endpoints are read-only and serve JSON. Start the server:

```bash
sep47idx serve --network testnet --addr :8080
```

Then query the contracts endpoint:

```bash
curl "http://localhost:8080/v1/contracts?implements=41&tier=declared&network=testnet"
```

**Example response** (truncated):

```json
{
  "contracts": [
    {
      "contract_id": "CCD1234567890abcdef1234567890abcdef12",
      "network": "testnet",
      "kind": "wasm",
      "wasm_hash": "7b4bb7b261286b436094af4684b3e18c961288e31705c51f743e3165bfbedb5d",
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
          "verified": null,
          "protocol": false,
          "source": "sep47-meta",
          "anomaly": null
        }
      ],
      "history": [
        {
          "wasm_hash": "7b4bb7b261286b436094af4684b3e18c961288e31705c51f743e3165bfbedb5d",
          "from_ledger": 10,
          "to_ledger": null,
          "exec_ref": null
        }
      ],
      "parser_version": "1",
      "as_of_ledger": 5927658
    }
  ],
  "pagination": {
    "next_cursor": null
  }
}
```

Similarly, for the API's `/v1/wasm/{hash}` endpoint:

```bash
curl "http://localhost:8080/v1/wasm/7b4bb7b261286b436094af4684b3e18c961288e31705c51f743e3165bfbedb5d"
```

**Example response**:

```json
{
  "hash": "7b4bb7b261286b436094af4684b3e18c961288e31705c51f743e3165bfbedb5d",
  "meta": {
    "has_sep": true,
    "seps": [41],
    "anomalies": []
  },
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
  "anomalies": [],
  "matches": {
    "status": "match",
    "ruleset_version": "sep41-v0.5.2",
    "ok_count": 10,
    "missing": [],
    "mismatched": [],
    "matched_variants": {"transfer.to": "MuxedAddress"}
  },
  "contracts_using": 3
}
```

## 6. Understanding the three tiers side-by-side

Run the same contract with all three tiers to see how the claims differ:

```bash
# Declared tier
sep47idx query --implements 41 --tier declared --network testnet --json | jq '.contracts[0].claims[0]'

# Inferred tier  
sep47idx query --implements 41 --tier inferred --network testnet --json | jq '.contracts[0].claims[0]'

# Protocol tier (SACs only)
sep47idx query --implements 41 --tier protocol --network testnet --json
```

**Key differences:**

| Field | Declared | Inferred | Protocol |
|-------|----------|----------|----------|
| `sep` present | Yes | Yes (if interface matches) | Yes (by kind) |
| `declared` flag | `true` | `false` (if no sep meta) | `false` (SACs are never `declared`) |
| `inferred.status` | `match`/`partial`/`mismatch`/`no_spec` | `match`/`partial`/`mismatch`/`no_spec` | `null` |
| `protocol` flag | `false` | `false` | `true` (SACs only) |

## 7. Why the index leads with "inferred"

On mainnet (7 October 2026), only 13 of 2178 unique Wasm hashes (0.60%) declare any SEP via `sep` meta. Fewer still — 16 of 141977 contracts (0.01%). However, 120 hashes match SEP-41's interface, and 1141 contracts match. The index therefore leads with **what code matches**, and tracks declarations as adoption grows.

## Commands summary

| Goal | CLI command | API endpoint |
|------|-------------|--------------|
| Phase 0 census | `sep47idx phase0 --network testnet --sample 2000 --out report/` | N/A |
| Declared SEP-41 | `sep47idx query --implements 41 --tier declared --network testnet` | `GET /v1/contracts?implements=41&tier=declared&network=testnet` |
| Inferred SEP-41 | `sep47idx query --implements 41 --tier inferred --network testnet` | `GET /v1/contracts?implements=41&tier=inferred&network=testnet` |
| Protocol SEP-41 | `sep47idx query --implements 41 --tier protocol --network testnet` | `GET /v1/contracts?implements=41&tier=protocol&network=testnet` |
| Wasm details | `sep47idx wasm <hash> --network testnet` | `GET /v1/wasm/{hash}?network=testnet` |
| Stats (adoption) | `sep47idx stats --network testnet` | `GET /v1/stats?network=testnet` |

## Next steps

- Add `contractmeta!(key="sep", val="41")` to your own contract's Rust source to make it `declared`.
- Use `sep47idx wasm <hash> --network testnet` to inspect a specific contract's meta and spec.
- Read the [trust tiers](trust-tiers.md) for exact definitions.