#!/usr/bin/env bash
# Compare two release builds of the same tag. Exits 0 when their SHA256SUMS
# are identical. Otherwise prints each differing file and, for each differing
# archive, the `go version -m` difference of its sep47idx binary, then exits 1.
#
#   scripts/compare-release.sh dist-a/v0.2.0 dist-b/v0.2.0
#
# Local only: does not tag, push, publish or call GitHub.
set -euo pipefail
[[ $# -eq 2 ]] || { echo 'usage: compare-release.sh <dir-a> <dir-b>' >&2; exit 2; }
a="$1" b="$2"
for d in "$a" "$b"; do
  [[ -f "$d/SHA256SUMS" ]] || { echo "$d/SHA256SUMS not found" >&2; exit 2; }
done
if cmp -s "$a/SHA256SUMS" "$b/SHA256SUMS"; then
  echo "reproducible: SHA256SUMS identical"
  cat "$a/SHA256SUMS"
  exit 0
fi
echo "NOT reproducible: SHA256SUMS differ" >&2
diff -u "$a/SHA256SUMS" "$b/SHA256SUMS" >&2 || true
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# Every file named in either list whose checksum is not the same in both.
names="$(cat "$a/SHA256SUMS" "$b/SHA256SUMS" | awk '{print $2}' | sort -u)"
for name in $names; do
  sa="$(awk -v n="$name" '$2 == n {print $1}' "$a/SHA256SUMS")"
  sb="$(awk -v n="$name" '$2 == n {print $1}' "$b/SHA256SUMS")"
  [[ "$sa" == "$sb" ]] && continue
  echo "differs: $name (a=${sa:-missing} b=${sb:-missing})" >&2
  [[ -f "$a/$name" && -f "$b/$name" && "$name" == *.tar.gz ]] || continue
  mkdir -p "$work/a/$name" "$work/b/$name"
  tar -xzf "$a/$name" -C "$work/a/$name"
  tar -xzf "$b/$name" -C "$work/b/$name"
  diff -r -q "$work/a/$name" "$work/b/$name" >&2 || true
  go version -m "$work/a/$name/sep47idx" > "$work/a/$name.mod" 2>&1 || true
  go version -m "$work/b/$name/sep47idx" > "$work/b/$name.mod" 2>&1 || true
  # The first line names the file path, which differs by construction.
  sed -i.bak '1s/^[^:]*://' "$work/a/$name.mod" "$work/b/$name.mod"
  rm -f "$work/a/$name.mod.bak" "$work/b/$name.mod.bak"
  echo "go version -m difference for $name:" >&2
  if diff -u "$work/a/$name.mod" "$work/b/$name.mod" >&2; then
    echo "  (build info identical; the difference is elsewhere in the archive)" >&2
  fi
done
exit 1
