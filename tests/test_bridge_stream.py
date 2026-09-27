"""Exercise actual pipe framing without network, UI or desktop effects."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from ui.bridge import REQUEST_BYTES

ROOT = Path(__file__).resolve().parents[1]


class BridgeStreamTests(unittest.TestCase):
    def run_stream(self, payload):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([sys.executable, "-I", "-S", "-B",
                str(ROOT / "ui/bridge.py"), "--state-dir", directory],
                input=payload, capture_output=True, timeout=5, check=True)
            self.assertEqual(result.stderr, b"")
            self.assertEqual(list(Path(directory).iterdir()), [])
            self.assertLess(len(result.stdout), 4096)
            return [json.loads(line) for line in result.stdout.splitlines()]

    def test_bad_json_then_valid_quit_remains_responsive(self):
        replies = self.run_stream(b'{broken\n{"op":"quit","request_id":7}\n')
        self.assertEqual(replies[0]["error"], "invalid_json")
        self.assertEqual(replies[1], dict(request_id=7, ok=True))

    def test_oversized_unterminated_frame_is_terminal(self):
        replies = self.run_stream(b"x" * (REQUEST_BYTES + 1))
        self.assertEqual(replies, [dict(request_id=None, ok=False,
            error="request_too_large")])

    def test_duplicate_fields_and_invalid_utf8_are_rejected(self):
        replies = self.run_stream(b'{"op":"quit","op":"snapshot"}\n\xff\n'
            b'{"op":"quit","request_id":8}\n')
        self.assertEqual([row.get("error") for row in replies[:2]],
            ["invalid_json", "invalid_json"])
        self.assertEqual(replies[2], dict(request_id=8, ok=True))
