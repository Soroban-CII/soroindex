#!/usr/bin/env bash
# Smoke-test one platform of a locally loaded image: it must report that
# platform, run as the nonroot user, and read an index through a read-only
# root filesystem and a read-only database mount.
# usage: smoke-image.sh IMAGE linux/amd64|linux/arm64
set -euo pipefail
[[ $# -eq 2 ]] || { echo 'usage: smoke-image.sh IMAGE linux/amd64|linux/arm64' >&2; exit 1; }
image="$1"
platform="$2"
arch="${platform#linux/}"
[[ "$platform" == "linux/$arch" && ( "$arch" == amd64 || "$arch" == arm64 ) ]] || { echo "unsupported platform $platform" >&2; exit 1; }

got="$(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}} user={{.Config.User}}')"
[[ "$got" == "$platform user=65532:65532" ]] || { echo "image is $got, want $platform user=65532:65532" >&2; exit 1; }

version="$(docker run --rm --read-only --platform "$platform" "$image" version)"
echo "$version"
[[ "$version" == *"linux/$arch"* ]] || { echo "binary does not report linux/$arch" >&2; exit 1; }

# Create an empty index with a writable mount (sync --recompute needs no RPC),
# then read it back with a read-only root filesystem and a read-only mount.
dir="$(mktemp -d)"
chmod 0777 "$dir"
trap 'rm -rf "$dir" 2>/dev/null || true' EXIT
docker run --rm --read-only --platform "$platform" --mount "type=bind,src=$dir,dst=/data" \
  "$image" sync --network testnet --db /data/testnet.db --recompute
out="$(docker run --rm --read-only --platform "$platform" --mount "type=bind,src=$dir,dst=/data,readonly" \
  "$image" stats --network testnet --db /data/testnet.db --json)"
echo "$out"
grep -q '"total_contracts": 0' <<<"$out" || { echo "stats did not read the empty index" >&2; exit 1; }
echo "smoke test passed for $platform"
