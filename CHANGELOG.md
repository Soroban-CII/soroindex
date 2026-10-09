# Changelog

## v0.1.0 — release candidate, 9 October 2026

- Parse SEP-47 metadata and SEP-48 interface specifications without executing Wasm.
- Infer SEP-41 interfaces using reviewed versioned rules; keep declared, inferred,
  protocol and deferred verified evidence separate.
- Seed and synchronize SQLite indexes with atomic resume, retention-gap detection,
  archival, upgrades and CAP-85 reference fan-out.
- Query the index through the read-only HTTP API and CLI, with keyset pagination,
  JSON/CSV output and optional per-IP request limiting.
- Provide static linux/darwin amd64/arm64 builds and a non-root distroless Dockerfile.
- Record the dated mainnet adoption census and real testnet validation evidence.
- Use Go 1.27.2 and the patched compression dependency; enable security and nightly
  testnet integration workflows.

No verifier execution, hosted API, published container or GitHub release is
claimed until the operator completes publication. The deployed-fixture live
upgrade check was waived; offline tests cover upgrade behavior.
