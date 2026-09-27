"""Synthetic DRM counter tests, not GPU performance evidence."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.gpu_usage import accumulate_busy, parse_fdinfo, sample_gpu


class GpuUsageTests(unittest.TestCase):
    def text(self, busy):
        return f"drm-driver: amdgpu\ndrm-pdev: 0000:04:00.0\ndrm-client-id: 17\ndrm-engine-gfx: {busy} ns\n"

    def test_missing_identity_and_non_time_fields_are_not_gpu_usage(self):
        self.assertEqual(parse_fdinfo("drm-engine-gfx: 123 ns\n"), {})
        self.assertEqual(parse_fdinfo(self.text(123) + "drm-engine-capacity-gfx: 2\n"),
                         {("amdgpu", "0000:04:00.0", "17", "gfx"): 123})

    def test_shared_fds_and_processes_do_not_double_count(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            for pid in (1, 2):
                directory = root / str(pid) / "fdinfo"
                directory.mkdir(parents=True)
                (directory / "4").write_text(self.text(100))
                (directory / "5").write_text(self.text(110))
            with patch("scripts.gpu_usage.Path", return_value=root):
                values, inaccessible = sample_gpu([1, 2])
            self.assertEqual(list(values.values()), [110])
            self.assertEqual(inaccessible, 0)

    def test_regressing_counters_keep_highwater_and_new_clients_get_baseline(self):
        key = ("amdgpu", "0000:04:00.0", "17", "gfx")
        highwater, totals = {}, {}
        for value in (100, 120, 110, 130):
            accumulate_busy({key: value}, highwater, totals)
        self.assertEqual(highwater[key], 130)
        self.assertEqual(totals, {"amdgpu/0000:04:00.0/gfx": 30})


if __name__ == "__main__":
    unittest.main()
