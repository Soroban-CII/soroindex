#!/usr/bin/env bash
# Local artifacts only. Does not tag, push, publish or call GitHub.
set -euo pipefail
release_version="${1:-v0.1.0}"
[[ "$release_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'version must be vN.N.N' >&2; exit 1; }
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
release_go="${GO:-go}"
release_out="$repo_dir/dist/$release_version"
mkdir -p "$release_out"
export CGO_ENABLED=0 GOTOOLCHAIN=local
"$release_go" mod verify
for release_target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  release_os="${release_target%/*}" release_arch="${release_target#*/}"
  release_dir="$release_out/${release_os}_${release_arch}"
  mkdir -p "$release_dir"
  GOOS="$release_os" GOARCH="$release_arch" "$release_go" build -trimpath -ldflags "-s -w -X main.version=$release_version" -o "$release_dir/sep47idx" ./cmd/sep47idx
  cp LICENSE "$release_dir/LICENSE"
done
RELEASE_VERSION="$release_version" RELEASE_GO="$release_go" python3 scripts/package_release.py
printf 'local release artifacts: %s\n' "$release_out"
