# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub private vulnerability reporting](https://github.com/ciscokwiz/soroindex/security/advisories/new): open the repository's **Security** tab and choose **Report a vulnerability** (private vulnerability reporting). Do not open a public issue for a vulnerability.

Include what you found, how to reproduce it, and the commit or version affected. We aim to acknowledge a report within 7 days and to agree a disclosure date with you.

## Scope

In scope:

- **The parser** (`pkg/sepmeta`): it reads untrusted Wasm and XDR from the network. Any input that makes it panic, loop, allocate without bound, or return wrong data is a vulnerability.
- **Ingestion and sync** (`internal/ingest`, `internal/rpc`, `internal/store`): anything that makes the index record data the network does not contain, skip ledgers silently, or overstate a contract's trust tier.
- **The HTTP API** (`internal/api`): injection, denial of service, or data the API should not expose.

Out of scope: the deployed contracts the index describes, and third-party RPC providers.

## Status

**This project is unaudited.** It has not had an external security review.

The index reports what contracts declare and what their interfaces match. **Neither is a statement that a contract is safe.** The planned `verified` tier records that a contract passed soroban-guard's SEP-41 conformance checks on one Wasm hash: a pass means those checks passed. **It is not a security audit.**

Private reporting must be enabled by the operator with `scripts/repo-settings.sh` before publication. If the route is unavailable, contact maintainer `ciscokwiz` on Discord privately; do not post exploit details in an issue.
