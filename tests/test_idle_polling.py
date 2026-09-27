"""Check idle sleep and immediate transition to active supervision over a pipe."""
import os
import tempfile
import unittest
from unittest.mock import patch

from ui.bridge import Bridge, StateDirectory, read_requests


class IdlePollingTests(unittest.TestCase):
    def test_request_starting_work_shortens_pending_idle_deadline(self):
        with tempfile.TemporaryDirectory() as folder:
            state = StateDirectory(folder)
            bridge = Bridge(state)
            reader, writer = os.pipe()
            closed = False
            waits = []
            def ready(read, write, error, timeout):
                nonlocal closed
                waits.append(timeout)
                if len(waits) == 2:
                    os.close(writer)
                    closed = True
                return read, [], []
            try:
                self.assertEqual(bridge.heartbeat_interval(), 5)
                os.write(writer, b'{"op":"refresh"}\n')
                with os.fdopen(reader, "rb") as incoming:
                    with patch("ui.bridge.time.monotonic", return_value=0), patch("ui.bridge.select.select", side_effect=ready):
                        requests = read_requests(bridge, incoming)
                        self.assertEqual(next(requests), b'{"op":"refresh"}\n')
                        bridge.worker = object()  # Request started pending work.
                        with self.assertRaises(StopIteration):
                            next(requests)
                self.assertEqual(waits, [0, .5])
            finally:
                bridge.worker = None
                if not closed:
                    os.close(writer)
                bridge.close()
                state.close()
