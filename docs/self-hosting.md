# Self-hosting

A multi-stage Dockerfile is implemented and tested locally. It builds a static binary with Go 1.27.4 and runs in digest-pinned distroless as UID/GID 65532. No registry image or release binary has been published yet.

## Build

Go 1.27.4, with `CGO_ENABLED=0`; SQLite is the pure-Go `modernc.org/sqlite` driver.

```sh
git clone https://github.com/Soroban-CII/soroindex
cd soroindex
CGO_ENABLED=0 go build -o sep47idx ./cmd/sep47idx
```

## Configure

Every flag can come from an environment variable (see [CLI reference](cli.md)):

| Variable | Use |
| --- | --- |
| `SEP47IDX_NETWORK` | `mainnet`, `testnet`, or `futurenet` |
| `SEP47IDX_RPC_URL` | RPC endpoint; required for mainnet |
| `SEP47IDX_DB` | Database path |
| `SEP47IDX_RPC_TIMEOUT`, `SEP47IDX_RPC_CONCURRENCY` | RPC tuning |
| `SEP47IDX_ADDR` | HTTP listen address (default `:8080`) |
| `SEP47IDX_RATE_LIMIT` | Requests per minute per peer IP; 0 disables |
| `SEP47IDX_READ_HEADER_TIMEOUT` | Maximum time to read request headers (default 5s) |
| `SEP47IDX_READ_TIMEOUT` | Maximum time to read request body (default 20s) |
| `SEP47IDX_WRITE_TIMEOUT` | Maximum time to write response (default 20s) |
| `SEP47IDX_IDLE_TIMEOUT` | Maximum time to wait for next request on keep-alive (default 60s) |
| `SEP47IDX_MAX_HEADER_BYTES` | Maximum size of request headers in bytes (default 16384) |

Stellar's docs list mainnet RPC providers. Several public endpoints answered a 200-key `getLedgerEntries` call correctly on 7 October 2026, among them `https://soroban-rpc.mainnet.stellar.gateway.fm`, which the mainnet census used. Futurenet has a public SDF RPC at `https://rpc-futurenet.stellar.org`.

### HTTP server hardening

The server enforces timeouts and header limits to protect against slowloris and resource exhaustion attacks. The defaults are conservative for a public API; adjust them if clients need longer request or response times.

| Flag | Env var | Default | Purpose |
| --- | --- | --- | --- |
| `--read-header-timeout` | `SEP47IDX_READ_HEADER_TIMEOUT` | 5s | Limits how long a client can take to send headers. A slow header sender is disconnected. |
| `--read-timeout` | `SEP47IDX_READ_TIMEOUT` | 20s | Limits total time to read the request body. |
| `--write-timeout` | `SEP47IDX_WRITE_TIMEOUT` | 20s | Limits time to write the response. A slow reader is disconnected. |
| `--idle-timeout` | `SEP47IDX_IDLE_TIMEOUT` | 60s | Maximum idle keep-alive time before closing the connection. |
| `--max-header-bytes` | `SEP47IDX_MAX_HEADER_BYTES` | 16384 | Maximum combined size of request headers. Oversized headers are rejected. |

All timeouts accept Go duration strings (e.g. `30s`, `1m`). Set to `0` to disable a timeout, but this is not recommended for a public endpoint. Graceful shutdown on SIGTERM/SIGINT waits up to 10 seconds for in-flight requests to complete before forcing connections closed.

## Seed

An index starts from a seed: a CSV with a `contract_id` column. For mainnet, export Q2 of [`scripts/hubble-export.sql`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/hubble-export.sql) from BigQuery, or run `phase0` and use its `mainnet-contracts.csv`.

```sh
sep47idx sync --network mainnet --rpc-url <url> --seed mainnet-contracts.csv
```

On 7 October 2026 a seed of all 156,173 mainnet contracts through a public endpoint ran about 1.4 seconds per batch of 200 contracts; a resumed run over the same list took 18 minutes, and the database was 85 MB. Seeding is idempotent; if it stops, run it again.

## Backups

The index is one SQLite file per network in WAL mode. Back it up with `sqlite3 data/mainnet.db ".backup backup.db"` while the indexer runs, or copy the `.db` file while it is stopped. A database records its network passphrase and refuses to open for another network.

## Maintenance

`sep47idx maintenance` checks integrity, refreshes the query planner's
statistics (`ANALYZE`) and reclaims free pages. Run it monthly, after a large
reseed, and before taking a backup you intend to keep. The
[CLI reference](cli.md#maintenance) shows a captured run.

1. Stop the sync process (`sync --follow`). Maintenance takes the write lock and
   refuses to start while a sync batch holds it. A follow process idle between
   batches is not detected, and its next batch would fail after its 10 s busy
   timeout if maintenance were still running.
2. Run it against the same database and network as the sync:

   ```sh
   sep47idx maintenance --network mainnet --db data/mainnet.db
   ```

3. Exit 0 means every step passed. Exit 1 with `integrity_check` problems means
   the file is damaged: maintenance changed nothing else. Keep it for diagnosis,
   restore the last good backup, and resume sync from it.
4. Start sync again.

The API can keep serving throughout; it opens the database read-only.

A database created by `sync` has `auto_vacuum` off, so SQLite cannot vacuum it
incrementally, and maintenance says so. To switch it on, run once with
`--enable-incremental-vacuum`. That does one full `VACUUM`, which rewrites the
file and needs free disk about the size of the database. Later runs vacuum
incrementally inside the locked transaction.

`ANALYZE` changes the statistics the query planner uses. The query-plan tests
in `internal/store/queryplan_test.go` assert the plans on an analyzed fixture,
so an analyzed index plans the same way the tests check
([query plans](query-plans.md)).

## Run the API

Seed an index before serving. `serve` opens it read-only; run synchronization as a
separate process. The API does not migrate or populate the database. Mainnet
requires an explicit RPC URL and the RPC network must match the database.

The [CLI reference](cli.md) records the tested start command. The [API reference](api.md)
contains requests and responses from the real testnet seed.

## Docker validation

The local image `soroindex:stage-gh` was built on 9 October 2026. In this cloud
instance Docker needs the proxy hostname mapping and the proxy's trusted CA bundle.
The build uses the standard `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` build args,
and an optional BuildKit secret named `ca-certificates`. The CA mount is temporary;
TLS and Go module checksum verification remain enabled. Ordinary Internet builds
can omit that secret. Do not copy proxy credentials into a Dockerfile or image.

The database bind mount must be readable by UID 65532. The following command was
run with the seeded database and its WAL/SHM files present in that directory:

```text
$ docker image inspect --format 'user={{.Config.User}} image={{.Id}}' soroindex:stage-gh
user=65532:65532 image=sha256:f3f6c8233bbfa3ea50456aff668ff824412c8a25d17e280259df77d739eb5ae3
$ docker run --rm --read-only soroindex:stage-gh version
sep47idx dev (go1.27.2, linux/amd64)
$ docker run --rm --read-only --mount type=bind,src=/workspace/scratch/stage-g,dst=/data,readonly soroindex:stage-gh stats --db /data/testnet.db --json
...
  "total_contracts": 200,
  "last_ledger": 5097816,
...
```

A container API run also returned the stored statistics and a fresh RPC health
response. It used a read-only root filesystem, a read-only database mount and the
cloud proxy CA mounted read-only. SIGTERM completed with exit 0. The extracted
binary was reported by `file` as statically linked. These are local validations;
no hosted deployment or image publication is claimed.

For a database that is actively being synced, retain the SQLite WAL/SHM files
alongside the database. A frozen copy should be checkpointed before distribution.
Avoid mounting only the main `.db` file while its writer is active.

### Image size (9 November 2026)

The image was rebuilt from `main` (`3abe5a3` plus documentation commits) with
`VERSION=v0.1.0`. The proxy CA was passed as the `ca-certificates` build secret
and the proxy settings as build args, as described above:

```text
$ docker run --rm --read-only soroindex:check version
sep47idx v0.1.0 (go1.27.3, linux/amd64)
$ docker image inspect soroindex:check --format 'image size: {{.Size}} bytes, user {{.Config.User}}'
image size: 27772533 bytes, user 65532:65532
$ docker inspect soroindex:check --format 'env={{.Config.Env}}'
env=[PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt]
```

That is 27,772,533 bytes, about 26.5 MiB. Outside `/app/licenses`, the image
holds only `/app/sep47idx` and an empty `/app/data`. It contains no database,
no proxy settings and no CA bundle; `docker history` shows neither.

## Published images

`.github/workflows/release.yml` builds and pushes `ghcr.io/soroban-cii/soroindex:<tag>`:

- Pushing a `vN.N.N` tag builds the archives and the image, creates a **draft**
  GitHub release for a maintainer to review, and pushes the image.
- For a release that already exists, such as v0.1.0, run it from
  **Actions → release → Run workflow** with the tag. It never replaces published
  assets. It reports whether the rebuilt archives match the published
  `SHA256SUMS`, then pushes the image. v0.1.0's archives were built before the
  tag existed, so Go stamped them with a pseudo-version
  (`v0.0.0-20261109093629-3abe5a30038d`), and a rebuild at the tag cannot match
  them byte for byte. `sep47idx version` reports `v0.1.0` in both.

### Reproducible release builds

Every release run builds the archives twice, in two jobs (`build-a` and
`build-b`) on fresh runners with no Go build cache, from the same tag. The
`reproducible` job runs `scripts/compare-release.sh` on the two results and
fails unless their `SHA256SUMS` are identical. On a mismatch it prints each
differing file and the `go version -m` difference of the `sep47idx` binary in
each differing archive. The draft release and the image are only made after
that job passes, and the published archives are `build-a`'s.

To reproduce a release yourself, check out the tag (a detached checkout of
the tag, not a branch, so Go stamps the tag's version) with a clean tree, and
build with the Go version in `go.mod`'s `toolchain` line:

```sh
git clone https://github.com/Soroban-CII/soroindex && cd soroindex
git checkout vX.Y.Z
GOTOOLCHAIN=local ./scripts/build-release.sh vX.Y.Z   # needs go1.27.4 and python3
curl -fsSLO https://github.com/Soroban-CII/soroindex/releases/download/vX.Y.Z/SHA256SUMS
mkdir -p published && mv SHA256SUMS published/
./scripts/compare-release.sh published dist/vX.Y.Z
```

`compare-release.sh` only needs a directory with a `SHA256SUMS` file on one
side, so this compares your build with the published checksums; it extracts
archives for the `go version -m` difference only when both sides have them.
A tree with uncommitted changes records `"source_tree_dirty": true` in
`BUILD.json` and does not match. Releases made before this check (v0.1.0)
cannot be reproduced, as explained above.

A new GHCR package starts private. Make it public once, in the package's settings.

## Docker Compose deployment

A multi-container deployment stack is defined in `compose.yaml` to orchestrate background synchronization (`sync --follow`) alongside the read-only HTTP API server (`serve`) over a shared persistent volume.

### Security and Architecture

- **Shared Volume:** Both containers coordinate over the `soroindex-data` volume mounted at `/app/data`.
- **Read-Only API Mount:** The `serve` API container mounts the data volume as read-only (`/app/data:ro`), preventing any state modification by the API process.
- **Read-Only Root Filesystems:** Both containers run with `read_only: true` on their root filesystems and drop privileges to UID `65532:65532`.
- **Health Checks & Startup Order:** 
  - `sync` performs periodic health checks using `sep47idx stats`.
  - `serve` performs HTTP health probes against `/v1/health` via `sep47idx health`.
  - Startup order is coordinated via Docker Compose dependencies (`depends_on: sync: condition: service_healthy`) and `--wait-for-db 30s` to cleanly handle empty initial index states without crashing.
- **Restart Policies:** Both services declare `restart: unless-stopped`.

### Starting the stack

```sh
docker compose up -d
```

Check logs and health status:

```sh
docker compose ps
docker compose logs -f
```

Query the API once healthy:

```sh
curl http://127.0.0.1:8080/v1/health
curl http://127.0.0.1:8080/v1/stats
```
