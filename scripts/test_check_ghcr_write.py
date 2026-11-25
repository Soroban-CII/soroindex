import os
import unittest
from contextlib import redirect_stderr
from email.message import Message
from io import StringIO
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlsplit
from unittest.mock import patch

from check_ghcr_write import check_write_access, main


class Response:
    def __init__(self, status, body=b"", headers=None):
        self.status = status
        self.body = body
        self.headers = Message()
        for key, value in (headers or {}).items():
            self.headers[key] = value

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return None

    def read(self):
        return self.body


class CheckGHCRWriteTests(unittest.TestCase):
    def test_write_scope_is_probed_and_upload_cancelled(self):
        responses = [
            Response(200, b'{"token":"bearer"}'),
            Response(202, headers={"Location": "/v2/acme/index/blobs/uploads/id"}),
            Response(204),
        ]
        requests = []

        def open_url(request, timeout):
            self.assertEqual(timeout, 15)
            requests.append(request)
            return responses.pop(0)

        check_write_access("Acme/Index", "actor", "secret-token", open_url)

        self.assertEqual(len(requests), 3)
        query = parse_qs(urlsplit(requests[0].full_url).query)
        self.assertEqual(query["scope"], ["repository:acme/index:pull,push"])
        self.assertEqual(requests[1].method, "POST")
        self.assertEqual(requests[1].get_header("Authorization"), "Bearer bearer")
        self.assertEqual(requests[2].method, "DELETE")
        self.assertEqual(requests[2].get_header("Authorization"), "Bearer bearer")
        self.assertTrue(requests[2].full_url.startswith("https://ghcr.io/"))

    def test_upload_must_be_cancellable(self):
        responses = [
            Response(200, b'{"token":"bearer"}'),
            Response(202, headers={"Location": "https://other.example/upload"}),
        ]

        def open_url(request, timeout):
            return responses.pop(0)

        with self.assertRaisesRegex(ValueError, "unexpected upload location"):
            check_write_access("acme/index", "actor", "secret-token", open_url)

    def test_required_credentials_are_checked(self):
        with self.assertRaisesRegex(ValueError, "are required"):
            check_write_access("acme/index", "", "secret-token")

    def test_denied_preflight_points_to_package_settings_without_leaking_token(self):
        error = HTTPError("https://ghcr.io/token", 403, "denied", None, None)
        stderr = StringIO()
        with patch.dict(os.environ, {
                "GITHUB_REPOSITORY": "acme/index",
                "GITHUB_ACTOR": "actor",
                "GH_TOKEN": "secret-token",
            }), patch("check_ghcr_write.urlopen", side_effect=error), redirect_stderr(stderr):
            self.assertEqual(main(), 1)

        self.assertIn("package settings", stderr.getvalue())
        self.assertIn("GitHub Actions write access", stderr.getvalue())
        self.assertNotIn("secret-token", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
