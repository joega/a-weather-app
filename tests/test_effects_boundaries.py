"""Effects environment and cleanup checks that never load native code."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from ui.effects import child_environment, cleanup_owned_directory


class EffectsBoundaryTests(unittest.TestCase):
    def test_loader_and_python_injection_variables_not_inherited(self):
        with patch.dict(os.environ, {"LD_PRELOAD": "/tmp/inject.so",
            "LD_LIBRARY_PATH": "/tmp/libraries", "PYTHONPATH": "/tmp/python",
            "PYTHONSTARTUP": "/tmp/startup", "PATH": "/tmp/tools",
            "HYPRLAND_INSTANCE_SIGNATURE": "stale-session"}):
            environment = child_environment()
        for name in ("LD_PRELOAD", "LD_LIBRARY_PATH", "PYTHONPATH", "PYTHONSTARTUP",
                     "HYPRLAND_INSTANCE_SIGNATURE"):
            self.assertNotIn(name, environment)
        self.assertEqual(environment["PATH"], "/usr/bin:/bin")

    def test_unknown_files_prevent_cleanup_and_preserve_evidence(self):
        with tempfile.TemporaryDirectory() as parent:
            directory = Path(parent) / "effects"
            directory.mkdir(mode=0o700)
            info = directory.stat()
            evidence = directory / "foreign.txt"
            evidence.write_bytes(b"do not delete")
            with self.assertRaises(ValueError):
                cleanup_owned_directory(directory, (info.st_dev, info.st_ino))
            self.assertEqual(evidence.read_bytes(), b"do not delete")

    def test_replaced_directory_identity_prevents_cleanup(self):
        with tempfile.TemporaryDirectory() as parent:
            directory = Path(parent) / "effects"
            directory.mkdir(mode=0o700)
            info = directory.stat()
            directory.rename(Path(parent) / "original")
            directory.mkdir(mode=0o700)
            with self.assertRaises(ValueError):
                cleanup_owned_directory(directory, (info.st_dev, info.st_ino))
            self.assertTrue(directory.is_dir())
