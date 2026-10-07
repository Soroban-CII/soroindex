#!/usr/bin/env bash
# Rebuilds the golden Wasm fixtures in testdata/wasm from testdata/contracts.
#
# Needs: rustup (installs the toolchain pinned in rust-toolchain.toml),
# stellar-cli at STELLAR_CLI_VERSION, and Go. Normal builds and CI never run
# this; the built .wasm files are committed.
#
# The CLI version is checked because `stellar contract build` writes it into
# each Wasm's meta (`cliver`), so a different CLI changes the bytes.
set -euo pipefail

STELLAR_CLI_VERSION="28.0.0"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

got="$(stellar --version | awk 'NR==1 {print $2}')"
if [[ "$got" != "$STELLAR_CLI_VERSION" ]]; then
  echo "build-fixtures: need stellar-cli $STELLAR_CLI_VERSION, found $got" >&2
  exit 1
fi

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

(cd testdata/contracts && stellar contract build --locked --out-dir "$out")

for f in token_full_sep token_full_legacy token_partial not_token multi_sep; do
  cp "$out/$f.wasm" "testdata/wasm/$f.wasm"
done

# no_meta: same build pipeline, then the whole contractmetav0 section removed.
go run ./scripts/fixturetool strip -section contractmetav0 \
  "$out/no_meta.wasm" testdata/wasm/no_meta.wasm

# truncated: token_full_sep cut halfway through its contractmetav0 payload.
go run ./scripts/fixturetool truncate -section contractmetav0 \
  testdata/wasm/token_full_sep.wasm testdata/wasm/truncated.wasm

(cd testdata/wasm && shasum -a 256 ./*.wasm > SHA256SUMS)
cat testdata/wasm/SHA256SUMS
