#!/usr/bin/env python3
"""Verify GHCR write permission without publishing an image."""

import base64
import json
import os
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urljoin, urlsplit
from urllib.request import Request, urlopen

REGISTRY = "https://ghcr.io"


def check_write_access(repository, actor, token, open_url=urlopen):
    """Create and cancel an empty upload to verify the repository's push scope."""
    if not repository or "/" not in repository or not actor or not token:
        raise ValueError("GITHUB_REPOSITORY, GITHUB_ACTOR and GH_TOKEN are required")

    image = repository.lower()
    scope = f"repository:{image}:pull,push"
    token_url = f"{REGISTRY}/token?{urlencode({'service': 'ghcr.io', 'scope': scope})}"
    basic = base64.b64encode(f"{actor}:{token}".encode()).decode("ascii")
    request = Request(token_url, headers={"Authorization": f"Basic {basic}"})
    with open_url(request, timeout=15) as response:
        credentials = json.loads(response.read())
    if not isinstance(credentials, dict):
        raise ValueError("GHCR returned an invalid token response")
    bearer = credentials.get("token") or credentials.get("access_token")
    if not isinstance(bearer, str) or not bearer:
        raise ValueError("GHCR did not issue a push token")

    upload_url = f"{REGISTRY}/v2/{image}/blobs/uploads/"
    request = Request(
        upload_url,
        data=b"",
        headers={"Authorization": f"Bearer {bearer}", "Content-Length": "0"},
        method="POST",
    )
    with open_url(request, timeout=15) as response:
        if response.status != 202:
            raise ValueError(f"GHCR upload preflight returned HTTP {response.status}")
        location = response.headers.get("Location")
    if not location:
        raise ValueError("GHCR did not return an upload location")

    abort_url = urljoin(REGISTRY, location)
    parsed = urlsplit(abort_url)
    if parsed.scheme != "https" or parsed.netloc != "ghcr.io":
        raise ValueError("GHCR returned an unexpected upload location")
    request = Request(
        abort_url,
        headers={"Authorization": f"Bearer {bearer}"},
        method="DELETE",
    )
    with open_url(request, timeout=15) as response:
        if response.status != 204:
            raise ValueError(f"GHCR upload cancellation returned HTTP {response.status}")


def main():
    try:
        check_write_access(
            os.environ.get("GITHUB_REPOSITORY", ""),
            os.environ.get("GITHUB_ACTOR", ""),
            os.environ.get("GH_TOKEN", ""),
            open_url=urlopen,
        )
    except HTTPError as error:
        print(
            f"GHCR write preflight denied (HTTP {error.code}). Check package "
            "settings and grant this repository's GitHub Actions write access.",
            file=sys.stderr,
        )
        return 1
    except (URLError, TimeoutError, OSError, ValueError) as error:
        print(
            f"GHCR write preflight failed ({type(error).__name__}). Check package "
            "settings and grant this repository's GitHub Actions write access.",
            file=sys.stderr,
        )
        return 1

    print("GHCR write access confirmed; the temporary upload was canceled.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
