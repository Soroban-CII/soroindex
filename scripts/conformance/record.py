"""Record what stellar-cli and the Python stellar-sdk report for one Wasm.

Run by scripts/record-conformance.sh, which pins both tool versions; do not
run it directly. For each input module it writes one JSON file holding:

- stellar_cli.meta: the key/value pairs `stellar contract info meta` prints;
- stellar_cli.interface: the function entries `stellar contract info
  interface` prints, as the CLI's own JSON;
- py_stellar_sdk: ContractMeta.items() and supported_seps() in both modes,
  plus supported_seps() on each comma-separated part of every "sep" value
  on its own, so a Go test can compare token by token.

Only public tool output is recorded. The script never reimplements a tool's
parsing: a per-part verdict is the SDK's own answer for a contract whose
single sep entry is that part.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from pathlib import Path

from stellar_sdk import xdr
from stellar_sdk.sep.contract_meta import ContractMeta


def run_cli(cli: str, what: str, wasm: Path) -> dict:
    """Run `stellar contract info <what>` and return its JSON or its error."""
    proc = subprocess.run(
        [cli, "contract", "info", what, "--wasm", str(wasm), "--output", "json"],
        capture_output=True,
        text=True,
        check=False,
        timeout=120,
    )
    if proc.returncode != 0:
        lines = [l for l in proc.stderr.splitlines() if l.strip()]
        return {"ok": False, "error": lines[-1] if lines else f"exit {proc.returncode}"}
    out = proc.stdout.strip()
    return {"ok": True, "value": json.loads(out) if out else []}


def cli_meta(cli: str, wasm: Path) -> dict:
    r = run_cli(cli, "meta", wasm)
    if not r["ok"]:
        return r
    entries = [[e["sc_meta_v0"]["key"], e["sc_meta_v0"]["val"]] for e in r["value"]]
    return {"ok": True, "entries": entries}


def cli_interface(cli: str, wasm: Path) -> dict:
    r = run_cli(cli, "interface", wasm)
    if not r["ok"]:
        return r
    fns = []
    for e in r["value"]:
        f = e.get("function_v0")
        if f is None:
            continue
        fns.append(
            {
                "name": f["name"],
                "inputs": [i["type"] for i in f["inputs"]],
                "outputs": f["outputs"],
            }
        )
    return {"ok": True, "functions": fns}


def seps(meta: ContractMeta, strict: bool) -> dict:
    try:
        return {"ok": True, "seps": list(meta.supported_seps(strict=strict))}
    except Exception as e:  # the SDK raises ValueError or InvalidWasmError
        return {"ok": False, "error": f"{type(e).__name__}: {e}"}


def one_part(part: str) -> dict:
    entry = xdr.SCMetaEntry(
        kind=xdr.SCMetaKind.SC_META_V0,
        v0=xdr.SCMetaV0(key=b"sep", val=part.encode("utf-8")),
    )
    meta = ContractMeta((entry,))
    return {"part": part, "lenient": seps(meta, False), "strict": seps(meta, True)}


def py_sdk(wasm: bytes) -> dict:
    try:
        meta = ContractMeta.from_wasm(wasm)
    except Exception as e:
        return {"ok": False, "error": f"{type(e).__name__}: {e}"}
    try:
        items = [list(kv) for kv in meta.items()]
    except Exception as e:
        return {"ok": False, "error": f"{type(e).__name__}: {e}"}
    values = [v for k, v in items if k == "sep"]
    return {
        "ok": True,
        "items": items,
        "supported_seps": seps(meta, False),
        "supported_seps_strict": seps(meta, True),
        # One list per sep entry, one verdict per comma-separated part.
        "parts": [[one_part(p) for p in v.split(",")] for v in values],
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cli", required=True, help="path to the pinned stellar binary")
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--kind", required=True, choices=["fixture", "vector"])
    ap.add_argument("--name", required=True)
    ap.add_argument("wasm", type=Path)
    a = ap.parse_args()

    data = a.wasm.read_bytes()
    record = {
        "input": {
            "kind": a.kind,
            "name": a.name,
            "wasm_sha256": hashlib.sha256(data).hexdigest(),
        },
        "stellar_cli": {
            "meta": cli_meta(a.cli, a.wasm),
            "interface": cli_interface(a.cli, a.wasm),
        },
        "py_stellar_sdk": py_sdk(data),
    }
    a.out.write_text(json.dumps(record, indent=2, ensure_ascii=False, sort_keys=True) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
