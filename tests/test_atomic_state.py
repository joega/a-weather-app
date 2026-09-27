"""Atomic publication preserves unrelated files and cleans failed temporaries."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from weather.files import write_file
from ui.bridge import StateDirectory


class AtomicStateTests(unittest.TestCase):
    def test_output_symlink_is_replaced_without_touching_target(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            target = root / "unrelated"
            target.write_bytes(b"preserve")
            output = root / "cache"
            output.symlink_to(target)
            write_file(output, b"new data")
            self.assertEqual(target.read_bytes(), b"preserve")
            self.assertFalse(output.is_symlink())
            self.assertEqual(output.read_bytes(), b"new data")

    def test_short_writes_are_completed(self):
        original_write = os.write
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "cache"
            with patch("weather.files.os.write", side_effect=lambda fd, data:
                    original_write(fd, data[:1])):
                write_file(path, b"complete contents")
            self.assertEqual(path.read_bytes(), b"complete contents")
            self.assertEqual([p.name for p in Path(folder).iterdir()], ["cache"])

    def test_prepublication_failure_preserves_old_state_and_removes_temp(self):
        for bridge_state in (False, True):
            with self.subTest(bridge_state=bridge_state), tempfile.TemporaryDirectory() as folder:
                path = Path(folder) / "cache"
                path.write_bytes(b"old data")
                state = StateDirectory(folder) if bridge_state else None
                try:
                    with patch("os.replace", side_effect=OSError("synthetic publication failure")):
                        with self.assertRaises(OSError):
                            if state:
                                state.write("cache", {"value": 1}, 1024)
                            else:
                                write_file(path, b"new data")
                    self.assertEqual(path.read_bytes(), b"old data")
                    self.assertEqual([p.name for p in Path(folder).iterdir()], ["cache"])
                finally:
                    if state:
                        state.close()
