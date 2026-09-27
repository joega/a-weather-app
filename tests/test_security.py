"""Offline regression checks for untrusted provider and filesystem boundaries."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

from weather import runtime
from weather.files import read_file, write_file
from weather.provider import ForecastError, _records, forecast_url
from ui.bridge import Bridge, StateDirectory


class ProviderBoundaryTests(unittest.TestCase):
    def test_lazy_network_fetch_keeps_bounds_and_redirect_refusal(self):
        from weather.network import NoRedirect
        response = Mock()
        response.read.return_value = b'{"ok":true}'
        opened = Mock()
        opened.__enter__ = Mock(return_value=response)
        opened.__exit__ = Mock(return_value=False)
        opener = Mock()
        opener.open.return_value = opened
        with patch("urllib.request.build_opener", return_value=opener) as factory:
            self.assertEqual(runtime.fetch_json("https://api.open-meteo.com/v1/forecast"),
                {"ok": True})
        self.assertIsInstance(factory.call_args.args[0], NoRedirect)
        self.assertEqual(opener.open.call_args.kwargs["timeout"], 10)
        response.read.assert_called_once_with(runtime.MAX_BYTES + 1)
        with self.assertRaises(ValueError):
            NoRedirect().redirect_request(None, None, 302, "Found", {},
                "https://example.com/")

    def test_host_scheme_credentials_and_port_rejected_before_network(self):
        with patch("urllib.request.build_opener") as opener:
            for url in ("http://api.open-meteo.com/", "https://example.com/",
                        "https://api.open-meteo.com.evil.test/",
                        "https://user@api.open-meteo.com/",
                        "https://api.open-meteo.com:444/"):
                with self.subTest(url=url), self.assertRaises(ValueError):
                    runtime.fetch_json(url)
            opener.assert_not_called()

    def test_untrusted_json_rejected(self):
        for raw in (b'{"x":1,"x":2}', b'{"x":NaN}', b'{"x":1e999}',
                    b'{"x":' + b'[' * 10000 + b'0' + b']' * 10000 + b'}',
                    b'[]', b' ' * (runtime.MAX_BYTES + 1)):
            with self.subTest(prefix=raw[:30]), self.assertRaises(ValueError):
                runtime.decode_json(raw)

    def test_quoted_brackets_and_escapes_are_data(self):
        value = {"text": '[{\\"' * 100, "rows": [1, 2.5, None]}
        self.assertEqual(runtime.decode_json(json.dumps(value).encode()), value)

    def test_unknown_timezone_is_validation_error(self):
        location = dict(name="Test", latitude=0, longitude=0, timezone="Invalid/Zone")
        with self.assertRaises(ForecastError):
            forecast_url(location)

    def test_forecast_record_expansion_bounded(self):
        for section, count in (("hourly", 241), ("daily", 11)):
            with self.subTest(section=section), self.assertRaises(ForecastError):
                _records({section: {"time": list(range(count))}}, section, {})


class FileBoundaryTests(unittest.TestCase):
    def test_idle_snapshots_do_not_write_state_or_start_effects(self):
        with tempfile.TemporaryDirectory() as folder:
            state = StateDirectory(folder)
            bridge = Bridge(state)
            try:
                with patch.object(state, "write", wraps=state.write) as writes:
                    snapshot = bridge.snapshot(poll=False)
                    writes.assert_not_called()
                self.assertEqual(snapshot["effect_status"]["state"], "stopped")
                self.assertIsNone(bridge.supervisor)
                self.assertIsNone(bridge.worker)
            finally:
                bridge.close()
                state.close()

    def test_symlink_hardlink_and_oversize_inputs_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / "cache"
            write_file(target, b"{}")
            link = Path(folder) / "link"
            link.symlink_to(target)
            with self.assertRaises(OSError):
                read_file(link, 10)
            os.link(target, Path(folder) / "hardlink")
            with self.assertRaises(PermissionError):
                read_file(target, 10)
            os.unlink(Path(folder) / "hardlink")
            with self.assertRaises(PermissionError):
                read_file(target, 1)
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
