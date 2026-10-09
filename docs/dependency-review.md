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

A remaining module-only finding was GO-2026-5841 in `klauspost/compress` 1.17.6.
The affected `s2` package was not imported, but the dependency was updated to
1.18.7, the latest patched release in that minor line checked at the module proxy.
The broader 1.20 minor upgrade was unnecessary for this fix.

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
| github.com/stellar/go-stellar-sdk | v0.7.3 | Apache-2.0 |
| github.com/stellar/go-xdr | v0.0.0-20260806060815-dc590f17552a | ISC |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause; bundled notices |
| modernc.org/libc | v1.77.1 | BSD-3-Clause; bundled notices |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause; bundled notices |
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/klauspost/compress | v1.18.7 | BSD-3-Clause; bundled notices |
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
