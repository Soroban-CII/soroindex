#!/usr/bin/env python3
"""Print a Markdown table of headline Phase 0 numbers, before and after.

    scripts/report-summary-diff.py <before-summary.json> <after-summary.json>

Every number comes from the two summary files; nothing is computed beyond
reading them. A missing "before" file (first run) shows as "–".
"""
import json
import pathlib
import sys


def load(path):
    p = pathlib.Path(path)
    return json.loads(p.read_text()) if p.is_file() else None


def get(d, *keys):
    for k in keys:
        if not isinstance(d, dict) or k not in d:
            return None
        d = d[k]
    return d


def rate(d, *keys):
    r = get(d, *keys)
    if not r:
        return None
    return f"{r['n']} / {r['of']} ({r['pct']:.2f}%)"


ROWS = [
    ("Run", lambda s: get(s, "generated_at")),
    ("Latest ledger", lambda s: get(s, "latest_ledger")),
    ("Rules", lambda s: ", ".join(get(s, "ruleset_versions") or []) or None),
    ("Contracts counted", lambda s: get(s, "contracts", "total")),
    ("Wasm contracts measured", lambda s: get(s, "contracts", "measured")),
    ("Contracts by CAP-85 reference", lambda s: get(s, "contracts", "wasm_ref")),
    ("Unique hashes fetched", lambda s: get(s, "hashes", "measured")),
    ("Declares any SEP, by hash", lambda s: rate(s, "declares_any", "by_hash")),
    ("Declares any SEP, by contract", lambda s: rate(s, "declares_any", "by_contract")),
    ("SEP-41 `match`, contracts", lambda s: get(s, "sep41", "status", "match", "contracts")),
    ("Undeclared gap (inferred), contracts", lambda s: get(s, "sep41", "undeclared_gap", "contracts")),
]


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: report-summary-diff.py <before.json> <after.json>")
    before, after = load(sys.argv[1]), load(sys.argv[2])
    if after is None:
        sys.exit(f"{sys.argv[2]}: not found")
    print(f"| {after.get('network', '')} | Before | After |")
    print("| --- | ---: | ---: |")
    for label, f in ROWS:
        b = f(before) if before else None
        a = f(after)
        mark = "" if b == a else " ◀"
        print(f"| {label}{mark} | {'–' if b is None else b} | {'–' if a is None else a} |")


if __name__ == "__main__":
    main()
