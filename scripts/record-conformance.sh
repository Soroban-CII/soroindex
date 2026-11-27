#!/usr/bin/env bash
# Regenerate testdata/conformance/: what stellar-cli and the Python
# stellar-sdk report for every golden fixture and every meta vector.
# pkg/sepmeta/conformance_test.go compares sepmeta with these recordings, so
# normal `go test` needs neither tool.
#
# Both tools are pinned exactly; the script refuses any other version,
# because a different version may parse differently and the recordings
# would then silently describe a tool nobody runs.
#
# Needs: uv (fetches the pinned Python and SDK), Go, and stellar-cli at
# $STELLAR_CLI. The fixture build pins a different stellar-cli
# (scripts/build-fixtures.sh), so this one is passed by path:
#
#   STELLAR_CLI=/path/to/stellar-30.0.1/stellar ./scripts/record-conformance.sh
set -euo pipefail

STELLAR_CLI_VERSION="30.0.1"
STELLAR_SDK_VERSION="17.0.1"
PYTHON_VERSION="3.13"

cd "$(dirname "$0")/.."
STELLAR_CLI="${STELLAR_CLI:-stellar}"

got="$("$STELLAR_CLI" --version | head -1 | awk '{print $2}')"
if [ "$got" != "$STELLAR_CLI_VERSION" ]; then
	echo "record-conformance: need stellar-cli $STELLAR_CLI_VERSION at \$STELLAR_CLI, got '$got' ($STELLAR_CLI)" >&2
	exit 1
fi
command -v uv >/dev/null || { echo "record-conformance: uv is required" >&2; exit 1; }

out=testdata/conformance
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$out"
rm -f "$out"/*.json

py() {
	uv run --quiet --no-project --python "$PYTHON_VERSION" \
		--with "stellar-sdk==$STELLAR_SDK_VERSION" python -I "$@"
}

for wasm in testdata/wasm/*.wasm; do
	name="$(basename "$wasm" .wasm)"
	py scripts/conformance/record.py --cli "$STELLAR_CLI" --kind fixture --name "$name" \
		--out "$out/fixture-$name.json" "$wasm"
done

for vec in testdata/vectors/*.txt; do
	name="$(basename "$vec" .txt)"
	go run ./scripts/fixturetool metawasm "$vec" "$tmp/$name.wasm"
	py scripts/conformance/record.py --cli "$STELLAR_CLI" --kind vector --name "$name" \
		--out "$out/vector-$name.json" "$tmp/$name.wasm"
done

cli_full="$("$STELLAR_CLI" --version | head -1)"
py_full="$(py -c 'import sys; print(sys.version.split()[0])')"
sdk_full="$(py -c 'import stellar_sdk; print(stellar_sdk.__version__)')"
cat >"$out/VERSIONS.json" <<EOF
{
  "stellar_cli": "$cli_full",
  "stellar_sdk": "$sdk_full",
  "python": "$py_full"
}
EOF
echo "recorded $(ls "$out"/fixture-*.json | wc -l | tr -d ' ') fixtures and $(ls "$out"/vector-*.json | wc -l | tr -d ' ') vectors with $cli_full, stellar-sdk $sdk_full, Python $py_full"
