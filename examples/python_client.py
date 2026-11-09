#!/usr/bin/env python3
"""Typed Python client for the Soroban smart contract indexer (sep47idx).

Demonstrates:
  - Standard-library HTTP (urllib.request) with explicit request timeouts.
  - Distinguishing empty search results from 400 (Bad Request), 404 (Not Found)
    and 429 (Rate Limited) errors without conflating them.
  - Opaque cursor pagination across multi-page contract queries.
  - Distinguishing trust evidence across tiers: declared, inferred, verified, and protocol.
"""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Generator, List, Optional, TypedDict


class ApiError(Exception):
    """Base error returned by the indexer API."""

    def __init__(self, status_code: int, code: str, message: str) -> None:
        super().__init__(f"API error {status_code} ({code}): {message}")
        self.status_code = status_code
        self.code = code
        self.message = message


class BadRequestError(ApiError):
    """HTTP 400: Malformed contract ID, invalid Wasm hash, or bad filter parameter."""


class NotFoundError(ApiError):
    """HTTP 404: Unknown contract, absent resource, or missing endpoint."""


class RateLimitError(ApiError):
    """HTTP 429: Request rate limit exceeded."""

    def __init__(
        self, status_code: int, code: str, message: str, retry_after: Optional[int] = None
    ) -> None:
        super().__init__(status_code, code, message)
        self.retry_after = retry_after


class ContractSummary(TypedDict, total=False):
    contract_id: str
    kind: str  # "wasm", "wasm_ref", or "sac"
    wasm_hash: Optional[str]
    exec_ref: Optional[Dict[str, str]]
    archived: bool


class ContractsPage(TypedDict, total=False):
    contracts: List[ContractSummary]
    next_cursor: Optional[str]
    parser_version: str
    tier: str  # "declared", "inferred", "verified", or "protocol"
    ruleset_versions: Optional[Dict[str, str]]


class HealthInfo(TypedDict):
    status: str
    network: str
    last_ledger: int
    latest_ledger: int
    lag: int


class SoroIndexClient:
    """Client for sep47idx HTTP JSON API using Python standard library."""

    def __init__(self, base_url: str = "http://127.0.0.1:8080", timeout: float = 10.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        parsed = urllib.parse.urlparse(self.base_url)
        # Avoid routing local loopback fixture connections through environment proxies
        if parsed.hostname in ("127.0.0.1", "localhost"):
            self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        else:
            self.opener = urllib.request.build_opener()

    def _request(self, path: str, params: Optional[Dict[str, Any]] = None) -> Any:
        url = f"{self.base_url}{path}"
        if params:
            filtered = {k: v for k, v in params.items() if v is not None}
            if filtered:
                url += "?" + urllib.parse.urlencode(filtered)

        req = urllib.request.Request(
            url,
            headers={
                "Accept": "application/json",
                "User-Agent": "soroindex-python-client/1.0",
            },
            method="GET",
        )

        try:
            with self.opener.open(req, timeout=self.timeout) as resp:
                data = resp.read().decode("utf-8")
                return json.loads(data) if data else None
        except urllib.error.HTTPError as exc:
            err_code = "unknown"
            err_msg = exc.reason
            try:
                body = json.loads(exc.read().decode("utf-8"))
                if isinstance(body, dict) and "error" in body:
                    err_code = body["error"].get("code", "unknown")
                    err_msg = body["error"].get("message", err_msg)
            except Exception:
                pass

            if exc.code == 400:
                raise BadRequestError(exc.code, err_code, err_msg) from exc
            if exc.code == 404:
                raise NotFoundError(exc.code, err_code, err_msg) from exc
            if exc.code == 429:
                retry_header = exc.headers.get("Retry-After")
                retry_after = int(retry_header) if retry_header and retry_header.isdigit() else None
                raise RateLimitError(exc.code, err_code, err_msg, retry_after=retry_after) from exc
            raise ApiError(exc.code, err_code, err_msg) from exc

    def get_health(self) -> HealthInfo:
        """Fetch indexer and RPC health."""
        return self._request("/v1/health")

    def list_contracts(
        self,
        implements: Optional[List[int]] = None,
        implements_any: Optional[List[int]] = None,
        tier: str = "declared",
        kind: Optional[str] = None,
        limit: int = 50,
        cursor: Optional[str] = None,
    ) -> ContractsPage:
        """Query contracts by implemented SEPs and trust tier.

        Tiers:
          - 'declared': self-reported SEP declarations in contract bytecode metadata.
          - 'inferred': verified code interface matches against SEP function/type rules.
          - 'verified': behavioral execution / test-suite verification.
          - 'protocol': built-in Stellar Asset Contracts (SACs).
        """
        params: Dict[str, Any] = {
            "tier": tier,
            "limit": limit,
            "cursor": cursor,
            "kind": kind,
        }
        if implements:
            params["implements"] = ",".join(str(s) for s in implements)
        if implements_any:
            params["implements_any"] = ",".join(str(s) for s in implements_any)

        return self._request("/v1/contracts", params)

    def iter_contracts(
        self,
        implements: Optional[List[int]] = None,
        implements_any: Optional[List[int]] = None,
        tier: str = "declared",
        kind: Optional[str] = None,
        page_size: int = 50,
    ) -> Generator[ContractSummary, None, None]:
        """Paginates through all matching contracts following opaque next_cursor tokens."""
        cursor: Optional[str] = None
        while True:
            page = self.list_contracts(
                implements=implements,
                implements_any=implements_any,
                tier=tier,
                kind=kind,
                limit=page_size,
                cursor=cursor,
            )
            contracts = page.get("contracts") or []
            for c in contracts:
                yield c

            cursor = page.get("next_cursor")
            if not cursor:
                break


def demo(base_url: str) -> None:
    """Execute live queries against a fixture/running server and print structured evidence."""
    client = SoroIndexClient(base_url=base_url)

    print(f"Connecting to soroindex at {client.base_url}...")
    health = client.get_health()
    print(
        f"Health: status={health['status']} network={health['network']} "
        f"last_ledger={health['last_ledger']} latest_ledger={health['latest_ledger']} lag={health['lag']}"
    )

    print("\n--- 1. Declared Tier (self-reported in Wasm meta) ---")
    declared = list(client.iter_contracts(implements=[41], tier="declared", page_size=2))
    print(f"Found {len(declared)} contracts declaring SEP-41:")
    for c in declared:
        print(f"  - {c['contract_id']} (kind={c.get('kind')}, hash={c.get('wasm_hash')[:16]}...)")

    print("\n--- 2. Inferred Tier (interface rules matched from Wasm bytecode) ---")
    inferred = list(client.iter_contracts(implements=[41], tier="inferred", page_size=2))
    print(f"Found {len(inferred)} contracts with inferred match for SEP-41:")
    for c in inferred:
        wasm_h = c.get('wasm_hash')
        h_str = wasm_h[:16] + "..." if wasm_h else "none"
        print(f"  - {c['contract_id']} (kind={c.get('kind')}, hash={h_str})")

    print("\n--- 3. Protocol Tier (built-in Stellar Asset Contracts) ---")
    protocol = list(client.iter_contracts(implements=[41], tier="protocol"))
    print(f"Found {len(protocol)} protocol SACs implementing SEP-41:")
    for c in protocol:
        print(f"  - {c['contract_id']} (kind={c.get('kind')})")

    print("\n--- 4. Empty Result vs Error Distinctions ---")
    empty_res = client.list_contracts(implements=[999999], tier="declared")
    print(f"Empty query returned 200 with 0 items (not an error): {len(empty_res.get('contracts', []))} items")

    try:
        client._request("/v1/contracts/INVALID_NON_STRKEY")
    except BadRequestError as exc:
        print(f"Handled 400 Bad Request successfully: code={exc.code}, message={exc.message}")

    try:
        client._request("/v1/contracts/CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM")
    except NotFoundError as exc:
        print(f"Handled 404 Not Found successfully: code={exc.code}, message={exc.message}")


if __name__ == "__main__":
    target = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080"
    demo(target)
