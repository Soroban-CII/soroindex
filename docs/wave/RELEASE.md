# soroindex v0.1.0

An interface index and adoption tracker for Soroban contracts. Read SEP-47
claims, infer SEP-41 interfaces from typed specifications, and track changes
without executing contracts.

The mainnet census on 7 October 2026 counted 156,173 contracts. Of 2,178 live
Wasm hashes, 13 (0.60%) declared any SEP. 120 hashes matched SEP-41; 109 of those
declared nothing. This release leads with the inferred interface index. The
verifier is deferred under the recorded adoption decision.

- Read-only HTTP API and CLI with independent trust tiers, bounded pagination,
  JSON/CSV queries and optional request limiting.
- SQLite seeding and incremental sync with atomic resume, retention-gap guards,
  upgrades, archival and CAP-85 reference changes.
- Static archives for linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.
  Each archive includes the binary, license notices and build provenance.
- Go 1.27.2 and patched dependencies. See the recorded security scan and tests.

Verify downloaded archives against `SHA256SUMS`. The intended image tag is
`ghcr.io/ciscokwiz/soroindex:v0.1.0`. This body is prepared for operator publication;
the tag, image and GitHub release are not advertised as live before that step.

The 30-minute testnet follow check passed on 9 October 2026. The operator waived
the deployed-fixture upgrade check; offline tests cover version changes. No
hosted API is included. A verified pass means a suite's checks passed; it is not
a security audit. The project is unaudited.

Source and documentation: https://github.com/ciscokwiz/soroindex
