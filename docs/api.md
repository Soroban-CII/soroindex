# HTTP API

!!! warning "Not built yet"
    The HTTP API is designed but not implemented. This page describes the design so reviewers and contributors can see what is planned. Each endpoint is tracked as its own issue. Nothing below can be called today.

## Design

Read-only, JSON, under `/v1`. Keyset pagination with an opaque `cursor`; `limit` defaults to 50, maximum 500. Contract IDs are validated as `C...` strkeys and Wasm hashes as 64 lowercase hex characters before any query. The server opens the database read-only.

| Endpoint | Returns |
| --- | --- |
| `GET /v1/contracts` | Contracts currently satisfying filters: `implements` (comma list, AND), `implements_any` (OR), `tier` (`declared`, `inferred`, `verified`, `protocol`; default `declared`), `kind` (`wasm`, `wasm_ref`, `sac`) |
| `GET /v1/contracts/{id}` | Claims grouped by tier, version history, archived flag |
| `GET /v1/contracts/{id}/history` | Every Wasm version with its ledger range and the claims at each |
| `GET /v1/wasm/{hash}` | Parsed meta, claims, anomalies, interface matches, contracts using it |
| `GET /v1/seps` | Counts per SEP number by tier |
| `GET /v1/stats` | Adoption numbers, the undeclared gap, anomalies, last sync |
| `GET /v1/health` | `{status, network, last_ledger, latest_ledger, lag}` |

Errors always have the shape `{"error":{"code":"not_found|bad_request|rate_limited|internal","message":"..."}}`.

Any response carrying `verified` data comes with this caveat: a pass means the suite's checks passed; it is not a security audit.
