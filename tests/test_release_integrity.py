"""Artifact verification rejects tampering without executing native binaries."""
from copy import deepcopy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch

from ui.release import ARTIFACTS, manifest, verify_artifacts

spec = importlib.util.spec_from_file_location("weather_release_builder", Path(__file__).resolve().parents[1] / "packaging/release.py")
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class IntegrityTests(unittest.TestCase):
    def test_real_git_inventory_preserves_paths_and_finds_untracked_sources(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            subprocess.run(["/usr/bin/git", "init", "--quiet", folder], check=True)
            (root / " leading.py").write_text("# tracked source\n")
            (root / "new.py").write_text("# untracked source\n")
            (root / ".gitignore").write_text("ignored.py\n")
            (root / "ignored.py").write_text("# ignored scratch\n")
            subprocess.run(["/usr/bin/git", "-C", folder, "add", "--", " leading.py", ".gitignore"], check=True)
            with patch.object(release, "ROOT", root):
                self.assertEqual(release.source_files(), [" leading.py", "new.py"])
                with self.assertRaisesRegex(RuntimeError, "untracked release sources"):
                    release.create()

    def test_source_inventory_includes_untracked_modules_and_excludes_artifacts(self):
        listing = "ui/bridge.py\0weather/new.py\0README.md\0" + ARTIFACTS[0] + "\0ui/bridge.py\0"
        with patch.object(release, "command", return_value=listing) as command:
            self.assertEqual(release.source_files(), ["ui/bridge.py", "weather/new.py"])
        self.assertIn("--others", command.call_args.args)
        self.assertIn("--cached", command.call_args.args)

    def test_manifest_creation_refuses_untracked_sources_before_host_checks(self):
        with patch.object(release, "source_files", return_value=["weather/new.py"]), \
                patch.object(release, "command", return_value="weather/new.py\0") as command:
            with self.assertRaisesRegex(RuntimeError, "untracked release sources"):
                release.create()
        command.assert_called_once()

    def test_new_source_invalidates_existing_manifest_inventory(self):
        with patch.object(release, "manifest", return_value={"sources": {"ui/bridge.py": "old"}}), \
                patch.object(release, "verify_artifacts"), \
                patch.object(release, "source_files", return_value=["ui/bridge.py", "weather/new.py"]), \
                patch.object(release, "digest") as digest:
            with self.assertRaisesRegex(RuntimeError, "source inventory changed"):
                release.verify()
        digest.assert_not_called()

    def test_invalid_artifact_records_refused_before_file_read(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for record in (
                None, {}, {"bytes": True, "sha256": "a" * 64},
                {"bytes": 0, "sha256": "a" * 64},
                {"bytes": 16 * 1024 * 1024 + 1, "sha256": "a" * 64},
                {"bytes": 1, "sha256": "../untrusted"},
            ):
                with self.subTest(record=record):
                    metadata = dict(architecture="x86_64",
                        artifacts={ARTIFACTS[0]: record})
                    with self.assertRaises(ValueError):
                        verify_artifacts(ARTIFACTS[:1], root=root, metadata=metadata)

    def test_valid_artifact_then_same_length_tampering_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            path = root / ARTIFACTS[0]
            path.parent.mkdir(parents=True)
            data = b"synthetic-shader"
            path.write_bytes(data)
            metadata = dict(architecture="x86_64", artifacts={ARTIFACTS[0]:
                dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest())})
            verify_artifacts(ARTIFACTS[:1], root=root, metadata=metadata)
            path.write_bytes(b"X" * len(data))
            with self.assertRaises(RuntimeError):
                verify_artifacts(ARTIFACTS[:1], root=root, metadata=metadata)

    def test_boolean_schema_and_duplicate_fields_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "packaging").mkdir()
            path = root / "packaging/runtime.json"
            metadata = dict(schema_version=1, architecture="x86_64",
                source_commit="a" * 40, artifacts={name: {} for name in ARTIFACTS})
            path.write_text(json.dumps(metadata))
            self.assertEqual(manifest(root)["schema_version"], 1)
            bad = deepcopy(metadata)
            bad["schema_version"] = True
            path.write_text(json.dumps(bad))
            with self.assertRaises(ValueError):
                manifest(root)
            path.write_text('{"schema_version":1,' + json.dumps(metadata)[1:])
            with self.assertRaises(ValueError):
                manifest(root)
