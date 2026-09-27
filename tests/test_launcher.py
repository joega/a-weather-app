"""Exercise optional launcher installation in isolated data directories."""
import os
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import Mock, patch

from ui import launcher
from scripts import run_weather_app


class LauncherTests(unittest.TestCase):
    def test_toggle_uses_canonical_path_and_bounded_ipc(self):
        with patch.object(run_weather_app.subprocess, "run",
                return_value=Mock(returncode=0)) as execute:
            self.assertEqual(run_weather_app.main(["--toggle-window"]), 0)
        self.assertEqual(execute.call_args.args[0], ["/usr/bin/quickshell", "ipc",
            "--path", str(run_weather_app.ROOT / "ui/qml"), "call",
            "a-weather-app-ui", "toggleWindow"])
        self.assertEqual(execute.call_args.kwargs["timeout"], 2)
        self.assertFalse(execute.call_args.kwargs.get("shell", False))

    def test_toggle_timeout_is_controlled_failure(self):
        with patch.object(run_weather_app.subprocess, "run",
                side_effect=subprocess.TimeoutExpired("quickshell", 2)):
            self.assertEqual(run_weather_app.main(["--toggle-window"]), 1)

    def test_install_is_repeatable_with_quoted_executable(self):
        with tempfile.TemporaryDirectory(prefix="weather launcher ") as folder:
            with patch.dict(os.environ, {"XDG_DATA_HOME": folder}):
                self.assertEqual(launcher.install(), "installed")
                desktop = Path(folder) / "applications/a-weather-app.desktop"
                original = desktop.read_bytes()
                self.assertIn(f'Exec="{launcher.ROOT}/a-weather-app"\n'.encode(), original)
                self.assertNotIn(b"TryExec=", original)
                self.assertEqual(launcher.install(), "installed")
                self.assertEqual(desktop.read_bytes(), original)

    def test_conflicting_launcher_is_preserved(self):
        with tempfile.TemporaryDirectory() as folder:
            applications = Path(folder) / "applications"
            applications.mkdir()
            desktop = applications / "a-weather-app.desktop"
            desktop.write_bytes(b"unrelated user launcher")
            with patch.dict(os.environ, {"XDG_DATA_HOME": folder}):
                self.assertEqual(launcher.install(), "conflict")
            self.assertEqual(desktop.read_bytes(), b"unrelated user launcher")

    def test_symlinked_icon_is_not_followed_or_replaced(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            target = root / "unrelated"
            target.write_bytes(b"preserve me")
            icons = root / "icons/hicolor/scalable/apps"
            icons.mkdir(parents=True)
            icon = icons / "a-weather-app.svg"
            icon.symlink_to(target)
            with patch.dict(os.environ, {"XDG_DATA_HOME": folder}):
                self.assertEqual(launcher.install(), "failed")
            self.assertTrue(icon.is_symlink())
            self.assertEqual(target.read_bytes(), b"preserve me")
            self.assertFalse((root / "applications/a-weather-app.desktop").exists())

    def test_field_code_and_relative_data_paths_refused(self):
        for folder in ("/tmp/weather%f", "relative/weather"):
            with self.subTest(folder=folder), patch.dict(os.environ, {"XDG_DATA_HOME": folder}):
                self.assertIn(launcher.install(), ("unsupported_path", "failed"))
