from copy import deepcopy
import json
from pathlib import Path
import tempfile
import unittest

from weather.provider import DEFAULT_LOCATION
from weather.runtime import read_cache, select_weather, validate_snapshot


class CacheTests(unittest.TestCase):
    def snapshot(self):
        return dict(schema_version=1, location=deepcopy(DEFAULT_LOCATION),
            fetched_at="2026-09-27T12:00:00Z",
            current=dict(time="2026-09-27T12:00:00Z", condition="clear"),
            hourly=[], daily=[], alerts=dict(status="unavailable", items=[]))

    def test_malformed_cache_is_ignored(self):
        cases = [dict(schema_version=True), dict(hourly=[None]),
            dict(daily=[{}] * 11), dict(hourly=[{}] * 241),
            dict(alerts=[]), dict(alerts=dict(items={})),
            dict(alerts=dict(items=[None])),
            dict(alerts=dict(items=[dict(expires="not-a-date")]))]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "forecast.json"
            for updates in cases:
                with self.subTest(updates=updates):
                    snapshot = self.snapshot()
                    snapshot.update(updates)
                    path.write_text(json.dumps(snapshot))
                    self.assertIsNone(read_cache(path, DEFAULT_LOCATION))

    def test_accepted_minimal_cache_can_be_selected(self):
        snapshot = self.snapshot()
        self.assertIs(validate_snapshot(snapshot, DEFAULT_LOCATION), snapshot)
        self.assertEqual(select_weather(snapshot)["forecast"]["alerts"]["items"], [])
