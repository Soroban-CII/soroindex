# Dependency and security review

Reviewed on 9 October 2026. The application still has two direct Go modules:
Stellar's SDK for XDR/strkeys and the pure-Go SQLite driver. The API and CLI add
only standard-library imports. No Wasm execution engine or verifier was added.

## Vulnerability scan

`govulncheck` 1.8.0 was checked at proxy.golang.org and installed with module
checksum verification. The cloud network denied `https://vuln.go.dev`, including
a read-only retry. The scan instead used **unmodified OSV files from the official
golang/vulndb repository**, snapshot
`f5aaa67f6ede4dff5b467dd6beba251f3c706b05`, obtained over HTTPS on 9 October.
The tool supports a local flat directory of OSV files. No advisory was removed
or suppressed. This is a snapshot scan; CI uses the live database.

The first scan found nine reachable standard-library advisories with Go 1.27.1.
Go's official release metadata identified 1.27.2 as the current stable patch.
The verified Linux archive checksum was
`ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5`.
Updating the build pin removed those findings. The language floor stays 1.26.0.

On 16 November 2026 both govulncheck jobs reported GO-2026-6859 (HTTP/1 server
crash due to a `MaxBytesReader` header race in `net/http`), found in the go1.27.3
standard library and fixed in go1.27.4. The symbol scan traces it through the RPC
client's response reads and the API server's response writes. The build pin moved to
go1.27.4 in `go.mod`, every workflow and the Dockerfile (`golang:1.27.4-bookworm`
index digest `sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea`).
The language floor is unchanged.

A remaining module-only finding was GO-2026-5841 in `klauspost/compress` 1.17.6.
The affected `s2` package was not imported, but the dependency was updated to
1.18.7, the latest patched release in that minor line checked at the module proxy.
The broader 1.20 minor upgrade was unnecessary for this fix.

On 8 November 2026 the CI scan reported GO-2026-6865 (panic on two-block
frames) in 1.18.7. It is reachable through the SDK's `xdr` stream decoder. The
fix is first in v1.20.2, and no 1.18 patch release carries it, so the module
moved to v1.20.3, the newest 1.20 patch at the module proxy.

Final output:

```text
$ govulncheck -db file:///workspace/scratch/stage-g/vulndb/data/osv ./...
No vulnerabilities found.
```

The full offline suite and race suite passed with Go 1.27.2 and the patched
dependency. `go mod tidy -diff`, `go vet ./...` and `go mod verify` passed.

```text
$ golangci-lint run ./...
0 issues.
```

This linter enables `gosec`, `govet`, `staticcheck`, `errcheck` and `revive`.
Fixed SQL fragments bind public values; the server requires a read-only store,
request deadlines and bounded pagination. The optional limiter uses the peer IP,
ignores untrusted forwarding headers, and bounds its in-memory windows.

## Runtime module inventory

Obtained from `go list -deps` for `./cmd/sep47idx`; root license files were read.
Generated SQLite/libc and compression modules also ship third-party notices;
retain those notices when redistributing dependencies.

| Module | Version | Root license |
| --- | --- | --- |
| github.com/stellar/go-stellar-sdk | v0.8.0 | Apache-2.0 |
| github.com/stellar/go-xdr | v0.0.0-20260806060815-dc590f17552a | ISC |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause; bundled notices |
| modernc.org/libc | v1.77.1 | BSD-3-Clause; bundled notices |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause; bundled notices |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/klauspost/compress | v1.20.3 | BSD-3-Clause; bundled notices |
| github.com/pkg/errors | v0.9.1 | BSD-2-Clause |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause |

`security.yml` runs the live reachable-code vulnerability scan on PRs, main
pushes and manual runs. Its PR-only `dependency-review` job independently runs
`govulncheck -C cmd/sep47idx -scan=module -show=verbose`, blocking known vulnerable
module versions even when the vulnerable code is not reachable. Unlike GitHub's
dependency-review action, it does not require Dependency Graph to be enabled on
the base repository. It checks the current Go dependency set against the live Go
vulnerability database, rather than comparing GitHub dependency snapshots or
filtering findings by severity. Neither job uses `continue-on-error`.

The command runs from the CLI package because the repository root has no Go
files, and module scanning does not accept `./...` patterns. Local validation
used the unmodified official database snapshot described above; the CI commands
use the live database. Hosted results must be checked after pushing.

## OpenSSF Scorecard review and findings

The repository runs the OpenSSF Scorecard action on weekly schedule and on `main` pushes (`.github/workflows/scorecard.yml`), publishing results to the OpenSSF Scorecard API and GitHub Code Scanning.

### Findings and remediation status

1. **Token-Permissions:**
   - *Status:* Remediated.
   - *Implementation:* All workflow definitions declare minimum permissions (`permissions: contents: read` or `permissions: read-all`) at the top level and grant specific scopes only where needed (e.g. `pages: write`, `id-token: write`).
2. **Pinned-Dependencies:**
   - *Status:* Remediated.
   - *Implementation:* All GitHub Actions are pinned to immutable 40-character commit SHAs with version comments (e.g. `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1`).
3. **Branch-Protection:**
   - *Status:* Documented and configured.
   - *Implementation:* Branch protection for `main` is automated via `scripts/repo-settings.sh`, requiring 1 pull request review approval, blocking force pushes, disallowing deletions, and requiring status checks (`lint`, `test`, `fuzz-smoke`, `build`). The public Scorecard check in GitHub Actions without a PAT with repository administration scope cannot query the GitHub Branch Protection API directly; this policy is maintained and enforced via repository configuration.
4. **Binary-Artifacts:**
   - *Status:* Documented by design.
   - *Implementation:* Static golden `.wasm` test fixtures are stored in `testdata/wasm/` for deterministic offline testing of SEP metadata parsing without network access or local Rust compiler installations. All fixture hashes are tracked in `testdata/wasm/SHA256SUMS`, and their source code is included under `testdata/contracts/` along with `scripts/build-fixtures.sh`.
5. **Dependency-Update-Tool:**
   - *Status:* Remediated.
   - *Implementation:* Dependabot is configured in `.github/dependabot.yml` to check Go modules and GitHub Actions weekly.
6. **Vulnerabilities:**
   - *Status:* Remediated / Monitored.
   - *Implementation:* Continuous vulnerability scanning is enforced on every PR and main push via `govulncheck` in `.github/workflows/security.yml`.
7. **Security-Policy:**
   - *Status:* Remediated.
   - *Implementation:* Documented in `SECURITY.md` with GitHub Private Vulnerability Reporting enabled.
8. **Fuzzing:**
   - *Status:* Remediated.
   - *Implementation:* Native Go fuzz tests for untrusted parser and ingest inputs run as smoke checks on PRs (`fuzz-smoke` in `ci.yml`) and in an extended nightly workflow with corpus caching (`.github/workflows/fuzz.yml`).
