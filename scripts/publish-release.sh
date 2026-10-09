#!/usr/bin/env bash
# Operator only. Default prints publication steps; --publish performs them.
set -euo pipefail
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
[[ $# -le 1 ]] || { echo 'usage: publish-release.sh [--publish]' >&2; exit 1; }
release_repo="${REPO:-ciscokwiz/soroindex}"
release_version=v0.1.0
release_image="ghcr.io/${release_repo,,}:$release_version"
if [[ "${1:-}" != --publish ]]; then
  [[ $# == 0 ]] || { echo 'usage: publish-release.sh [--publish]' >&2; exit 1; }
  printf 'Operator publication plan for %s at the reviewed main commit:\n' "$release_repo"
  printf '1. Require clean main; rebuild four static archives and verify SHA256SUMS.\n'
  printf '2. Build and smoke-test %s (requires configured registry login).\n' "$release_image"
  printf '3. Create/push annotated %s tag without overwriting any existing tag.\n' "$release_version"
  printf '4. Push image and create the GitHub release with archives, checksums and docs/wave/RELEASE.md.\n'
  exit 0
fi
[[ "$release_repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo 'REPO must be owner/name' >&2; exit 1; }
[[ "$(git branch --show-current)" == main ]] || { echo 'merge and review main before publishing' >&2; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo 'tracked/untracked changes must be committed first' >&2; exit 1; }
release_origin="$(git remote get-url origin)"
[[ "$release_origin" == "https://github.com/$release_repo.git" || "$release_origin" == "https://github.com/$release_repo" || "$release_origin" == "git@github.com:$release_repo.git" ]] || { echo 'origin does not match REPO' >&2; exit 1; }
command -v gh >/dev/null
command -v docker >/dev/null
./scripts/build-release.sh "$release_version"
(cd "dist/$release_version" && sha256sum --check SHA256SUMS)
docker build --build-arg "VERSION=$release_version" -t "$release_image" .
docker run --rm --read-only "$release_image" version
if git rev-parse --verify "refs/tags/$release_version" >/dev/null 2>&1; then
  [[ "$(git rev-parse "$release_version^{commit}")" == "$(git rev-parse HEAD)" ]] || { echo 'existing tag points elsewhere; refusing overwrite' >&2; exit 1; }
else
  git tag -a "$release_version" -m 'soroindex v0.1.0' HEAD
fi
git push origin "refs/tags/$release_version"
docker push "$release_image"
if gh release view "$release_version" --repo "$release_repo" >/dev/null 2>&1; then
  echo 'release already exists; do not overwrite reviewed release assets'
else
  gh release create "$release_version" --repo "$release_repo" --verify-tag --title 'soroindex v0.1.0' \
    --notes-file docs/wave/RELEASE.md "dist/$release_version/"*.tar.gz "dist/$release_version/SHA256SUMS" "dist/$release_version/BUILD.json"
fi
