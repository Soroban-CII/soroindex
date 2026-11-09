# Self-hosting

A multi-stage Dockerfile is implemented and tested locally. It builds a static binary with Go 1.27.3 and runs in digest-pinned distroless as UID/GID 65532. No registry image or release binary has been published yet.

## Build

Go 1.27.3, with `CGO_ENABLED=0`; SQLite is the pure-Go `modernc.org/sqlite` driver.

```sh
git clone https://github.com/Soroban-CII/soroindex
cd soroindex
CGO_ENABLED=0 go build -o sep47idx ./cmd/sep47idx
```

## Configure

Every flag can come from an environment variable (see [CLI reference](cli.md)):

| Variable | Use |
| --- | --- |
| `SEP47IDX_NETWORK` | `mainnet` or `testnet` |
| `SEP47IDX_RPC_URL` | RPC endpoint; required for mainnet |
| `SEP47IDX_DB` | Database path |
| `SEP47IDX_RPC_TIMEOUT`, `SEP47IDX_RPC_CONCURRENCY` | RPC tuning |

Stellar's docs list mainnet RPC providers. Several public endpoints answered a 200-key `getLedgerEntries` call correctly on 7 October 2026, among them `https://soroban-rpc.mainnet.stellar.gateway.fm`, which the mainnet census used.

## Seed

An index starts from a seed: a CSV with a `contract_id` column. For mainnet, export Q2 of [`scripts/hubble-export.sql`](https://github.com/Soroban-CII/soroindex/blob/main/scripts/hubble-export.sql) from BigQuery, or run `phase0` and use its `mainnet-contracts.csv`.

```sh
sep47idx sync --network mainnet --rpc-url <url> --seed mainnet-contracts.csv
```

On 7 October 2026 a seed of all 156,173 mainnet contracts through a public endpoint ran about 1.4 seconds per batch of 200 contracts; a resumed run over the same list took 18 minutes, and the database was 85 MB. Seeding is idempotent; if it stops, run it again.

## Backups

The index is one SQLite file per network in WAL mode. Back it up with `sqlite3 data/mainnet.db ".backup backup.db"` while the indexer runs, or copy the `.db` file while it is stopped. A database records its network passphrase and refuses to open for another network.

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

A new GHCR package starts private. Make it public once, in the package's settings.
