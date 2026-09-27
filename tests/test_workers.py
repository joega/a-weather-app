"""Real fork/deadline checks with no network, native effects, or user state."""
from datetime import datetime, timezone
import os
import signal
import tempfile
import time
import unittest
from unittest.mock import patch

from ui.bridge import RefreshWorker, StateDirectory
from weather.provider import DEFAULT_LOCATION


def failed_provider(location, now):
    raise OSError("synthetic provider outage")


def stalled_provider(location, now):
    while True:
        time.sleep(.01)


def uncooperative_provider(location, now):
    signal.signal(signal.SIGALRM, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    while True:
        time.sleep(.01)


class WorkerTests(unittest.TestCase):
    def run_worker(self, fetcher, expected):
        with tempfile.TemporaryDirectory() as folder:
            state = StateDirectory(folder)
            state.write("forecast.json", {"sentinel": "keep cached data"}, 1024)
            worker = RefreshWorker(state, fetcher, DEFAULT_LOCATION,
                                   lambda: datetime.now(timezone.utc))
            child = None
            started = time.monotonic()
            try:
                with patch("ui.bridge.FETCH_DEADLINE", .15):
                    worker.start()
                    child = worker.pid
                    result = None
                    while result is None and time.monotonic() - started < 5:
                        result = worker.poll()
                        time.sleep(.01)
                self.assertEqual(result, expected)
                self.assertIsNone(worker.pid)
                self.assertIsNone(worker.pipe)
                with self.assertRaises(ChildProcessError):
                    os.waitpid(child, os.WNOHANG)
                self.assertEqual(state.read("forecast.json", 1024),
                                 {"sentinel": "keep cached data"})
                self.assertLess(time.monotonic() - started, 5)
            finally:
                worker.stop()
                state.close()

    def test_provider_failure_retains_cache_and_reaps_child(self):
        self.run_worker(failed_provider, "refresh_failed")

    def test_stalled_provider_deadline_retains_cache_and_reaps_child(self):
        self.run_worker(stalled_provider, "fetch_timeout")

    def test_parent_deadline_kills_worker_ignoring_alarm_and_term(self):
        self.run_worker(uncooperative_provider, "fetch_timeout")


if __name__ == "__main__":
    unittest.main()
