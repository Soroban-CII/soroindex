# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub private vulnerability reporting](https://github.com/Soroban-CII/soroindex/security/advisories/new): open the repository's **Security** tab and choose **Report a vulnerability** (private vulnerability reporting). Do not open a public issue for a vulnerability.

Include what you found, how to reproduce it, and the commit or version affected. We aim to acknowledge a report within 7 days and to agree a disclosure date with you.

## Scope

In scope:

- **The parser** (`pkg/sepmeta`): it reads untrusted Wasm and XDR from the network. Any input that makes it panic, loop, allocate without bound, or return wrong data is a vulnerability.
- **Ingestion and sync** (`internal/ingest`, `internal/rpc`, `internal/store`): anything that makes the index record data the network does not contain, skip ledgers silently, or overstate a contract's trust tier.
- **The HTTP API** (`internal/api`): injection, denial of service, or data the API should not expose.

Out of scope: the deployed contracts the index describes, and third-party RPC providers.

### RPC response limits

Wasm and XDR are bounded by `pkg/sepmeta` limits, but the JSON-RPC envelope around them comes from the same untrusted provider. `internal/rpc` bounds it as follows, and a response that breaks a bound is an error, never a partial result:

| Hostile response | Limit | Behavior | Test (`internal/rpc/hostile_test.go`) |
| --- | --- | --- | --- |
| Declared `Content-Length` over the cap | `MaxResponseBytes`, default 256 MiB | `ErrResponseTooLarge` before any of the body is read | `TestHugeDeclaredBodyFailsBeforeRead` |
| Chunked or mis-declared body over the cap | `MaxResponseBytes` | Reading stops at cap + 1 byte; `ErrResponseTooLarge` | `TestHugeChunkedBodyIsCapped` |
| Deeply nested arrays or objects | `MaxJSONDepth`, default 64 | `ErrProtocol` from a non-allocating scan before decoding | `TestDeepNestingRejected`, `TestCheckDepth` |
| Invalid UTF-8 | — | `ErrProtocol`; `encoding/json` would otherwise replace it with U+FFFD | `TestInvalidUTF8Rejected` |
| Slow trickle | `Timeout` per attempt, default 30s | The attempt fails with a read-body timeout and is retried, up to `MaxAttempts` | `TestSlowTrickleTimesOut` |

Retries do not multiply memory: one attempt's body is released before the next starts, at most `Concurrency` attempts are in flight, and an over-size or malformed response is not retried, because the same request would get the same answer. The worst case held at once is therefore `Concurrency × (MaxResponseBytes + 1)` bytes of body.

## Status

**This project is unaudited.** It has not had an external security review.

The index reports what contracts declare and what their interfaces match. **Neither is a statement that a contract is safe.** The planned `verified` tier records that a contract passed soroban-guard's SEP-41 conformance checks on one Wasm hash: a pass means those checks passed. **It is not a security audit.**

Private reporting must be enabled by the operator with `scripts/repo-settings.sh` before publication. If the route is unavailable, contact maintainer `ciscokwiz` on Discord privately; do not post exploit details in an issue.
