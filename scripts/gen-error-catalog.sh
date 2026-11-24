#!/usr/bin/env bash
# Regenerate docs/errors.md from internal/clierr. The clierr test fails when
# the committed page is stale, so run this after changing the catalog.
set -euo pipefail
cd "$(dirname "$0")/.."
go run ./scripts/errorcatalog > docs/errors.md
