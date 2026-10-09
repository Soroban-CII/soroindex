# Contributing

Thanks for helping. This page covers setup, the checks your change must pass, how commits are written, and how Stellar Wave issues work.

## Setup

You need Go 1.27.2. `go.mod` pins it with `toolchain go1.27.2`, so an older supported Go can download it automatically. Nothing else is needed for normal work: no CGO, no Rust, no database server.

```sh
git clone https://github.com/ciscokwiz/soroindex
cd soroindex
make test
```

If `go` fails to download modules with "unknown revision" errors, your `GOPROXY` is probably `direct`. Some dependencies resolve only through the module proxy:

```sh
GOPROXY=https://proxy.golang.org,direct make test
```

## Make targets

| Target | What it runs | CI job |
| --- | --- | --- |
| `make test` | `go test ./...` | `test` (with `-race`) |
| `make lint` | `golangci-lint run ./...` (govet, staticcheck, errcheck, gosec, revive) | `lint` |
| `make rules-check` | Validates `rules/sep-*.json` against `rules/schema.json` | `lint` |
| `make tidy-check` | Fails if `go.mod`/`go.sum` are not tidy | `lint` |
| `make fuzz-smoke` | Each fuzz target for 30 s | `fuzz-smoke` |
| `make fuzz FUZZTIME=10m` | Each fuzz target for 10 minutes | — |
| `make build` | Static binary, `CGO_ENABLED=0` | `build` (linux/darwin × amd64/arm64) |
| `make fixtures` | Rebuilds `testdata/wasm` from Rust sources (needs Rust and stellar-cli) | — |
| `make docs` | `mkdocs build --strict` | `docs` workflow |

The required checks are `lint`, `test`, `fuzz-smoke` and `build`. Security and docs workflows provide additional review evidence.

## Rules for code

- Treat Wasm and XDR from the network as hostile. Check every length against the bytes remaining and a limit before allocating.
- No panics outside `main` setup and tests. Malformed input becomes a recorded error.
- Every I/O function takes a `context.Context` first, and every RPC call has a timeout.
- SQL is parameterized. Database writes are idempotent.
- Every exported identifier has a doc comment that says why.
- Tests are table-driven, with `t.Run` names that state the expected behavior. Tests do not touch the network unless they carry the `integration` build tag.
- New dependencies need a stated reason in the commit and a maintainer's agreement. The allowed set today is `github.com/stellar/go-stellar-sdk` (the `xdr` and `strkey` packages) and `modernc.org/sqlite`.
- Where a requirement is ambiguous, choose the reading that claims less trust about a contract.

## Commits

- One commit per logical change: one function with its tests, one migration, one endpoint, one doc page.
- [Conventional commits](https://www.conventionalcommits.org/), lowercase, imperative: `type(scope): description`.
  - Types: `feat fix test docs chore ci refactor perf build`.
  - Scopes: `sepmeta rpc ingest claims match store sync api cli verify phase0 docs repo`.
- Stage files by name; do not commit with `git add .`.
- A fix to something already merged is its own commit. Its body says what was wrong and how you showed it: a test that fails before the fix and passes after.
- Never commit secrets, `.env` files or databases (`.gitignore` covers them).

## Stellar Wave issues

Issues open for [Stellar Wave](https://www.drips.network/wave/stellar) contributors carry the label **`Stellar Wave`**, plus:

- a difficulty: `difficulty/trivial`, `difficulty/medium` or `difficulty/high`;
- an area: `area/parser`, `area/sync`, `area/api`, `area/cli`, `area/rules`, `area/docs`, `area/infra`.

To take one, comment on the issue and wait for a maintainer to assign it to you; one assignee per issue. Each issue lists acceptance criteria as checkboxes; a pull request should tick all of them, link the issue, and pass CI. Maintainers review Wave pull requests within 48 hours.

## Rule-file pull requests

A new or changed `rules/sep-NNNN.json`:

- [ ] Fetched the SEP's current text and named the commit you read in the PR description.
- [ ] Every required function and type matches the text; nothing optional is marked required.
- [ ] `source` names the SEP file, its version, its updated date, and the date you diffed it.
- [ ] `ruleset_version` changed if any rule changed.
- [ ] `make rules-check` passes.
- [ ] A test in `internal/match` covers at least one matching and one mismatching spec.

## Versions

Baseline checked on 7 October 2026. Go was refreshed against go.dev on 9 October
2026 to fix reachable standard-library advisories found with 1.27.1. The language
floor is unchanged. A bump is its own commit, with a test run.

| Component | Version | Pinned in |
| --- | --- | --- |
| Go toolchain (build) | go1.27.2 | `go.mod` `toolchain`, `ci.yml`, `Dockerfile` |
| Go language floor | 1.26 (the highest `go` directive in the dependency tree: modernc.org/sqlite v1.60.1) | `go.mod` `go` |
| github.com/stellar/go-stellar-sdk | v0.7.3 | `go.mod` |
| modernc.org/sqlite | v1.60.1 | `go.mod` |
| github.com/klauspost/compress (transitive) | v1.18.7 | `go.mod`; patched 1.18 release checked on 9 October 2026 |
| govulncheck | v1.8.0 | `.github/workflows/security.yml`; checked on 9 October 2026 |
| golangci-lint | v2.14.0 | `.github/workflows/ci.yml` |
| check-jsonschema | 0.38.2 | `ci.yml`, `Makefile` |
| MkDocs / Material | 1.6.1 / 9.7.7 | `docs/requirements.txt` |
| Rust (fixtures only) | 1.98.1 | `testdata/contracts/rust-toolchain.toml` |
| soroban-sdk, soroban-token-sdk (fixtures only) | 28.0.0 | `testdata/contracts/Cargo.toml` |
| stellar-cli (fixtures only) | 28.0.0 | `scripts/build-fixtures.sh` |

## Wave backlog and operator tools

`scripts/wave-issues.json` is the reviewed remaining-work list. The application
summary is generated from that source. Preview with `DRY_RUN=1 ./scripts/create-issues.sh`;
preview requires Python 3 and makes no GitHub calls. A maintainer creates the
labels and issues with `./scripts/create-issues.sh` after reviewing the preview.
Already-open or closed issues with identical titles are skipped. Do not add work
already implemented or proposed changes that bypass the fixed API/schema review.

Preview settings with `DRY_RUN=1 ./scripts/repo-settings.sh`. Only the operator
runs the actual settings script. Both scripts default to `ciscokwiz/soroindex`;
`REPO=owner/name` overrides the target explicitly.

Build local release archives with `./scripts/build-release.sh v0.1.0` after
activating Go 1.27.2. This requires Bash, Python 3, Git and Go; it does not publish.
The archives include checksums, provenance and dependency license notices.
`scripts/publish-release.sh` previews the publication sequence. Only the operator
runs it with `--publish`, after merging and reviewing a clean `main`, configuring
registry access and inspecting the release body. It never overwrites an existing
tag pointing at a different commit. A tag, image, GitHub release and docs site
must be checked while logged out before the Wave application is submitted.
