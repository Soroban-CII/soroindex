#!/usr/bin/env bash
# Operator-run utility. DRY_RUN=1 validates and previews without GitHub calls.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
args=(--repo "${REPO:-ciscokwiz/soroindex}")
if [[ "${DRY_RUN:-0}" != 1 ]]; then args+=(--create); fi
exec python3 "$script_dir/wave_issues.py" "${args[@]}"
