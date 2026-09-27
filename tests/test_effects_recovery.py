"""Crash recovery ownership checks; no compositor or native processes."""
from types import SimpleNamespace
import os
import json
import unittest
from unittest.mock import Mock, patch

from ui.effects_proxy import OwnedEffectsSupervisor
from ui.effects_worker import serve


class Backend:
    def __init__(self):
        self.actual_inventory = [{"name": "weather", "handle": "owned"}]
        self.generation = 7
        self.commands = []
        self.unloaded = False

    def identity(self):
        pass

    def inventory(self):
        return self.actual_inventory

    def native(self, command):
        self.commands.append(command)
        return dict(session_generation=self.generation,
                    enabled=command == "status", cleanup_failed=False)

    def unload(self):
        self.unloaded = True


class RecoveryTests(unittest.TestCase):
    def test_valid_worker_status_and_stop_remain_compatible(self):
        supervisor = Mock()
        supervisor.status.return_value = {"state": "stopped"}
        supervisor.stop.return_value = {"state": "stopped"}
        read_fd, write_fd = os.pipe()
        reply_fd, output_fd = os.pipe()
        try:
            os.write(write_fd, b'{"id":1,"op":"status"}\n{"id":2,"op":"stop"}\n')
            self.assertEqual(serve(lambda: supervisor, read_fd, output_fd), 0)
            replies = [json.loads(line) for line in os.read(reply_fd, 4096).splitlines()]
            self.assertEqual([reply["id"] for reply in replies], [1, 2])
            self.assertTrue(all(reply["ok"] for reply in replies))
            supervisor.stop.assert_called_once()
        finally:
            for fd in (read_fd, write_fd, reply_fd, output_fd):
                os.close(fd)

    def test_worker_rejects_ambiguous_requests_and_cleans_up(self):
        for request in (b'{"id":1,"op":"stop","op":"status"}\n',
                        b'{"id":true,"op":"stop"}\n',
                        b'{"id":1,"op":"tick","weather":NaN}\n'):
            with self.subTest(request=request):
                supervisor = Mock()
                supervisor.status.return_value = {"state": "stopped"}
                supervisor.stop.return_value = {"state": "stopped"}
                read_fd, write_fd = os.pipe()
                reply_fd, output_fd = os.pipe()
                try:
                    os.write(write_fd, request)
                    os.close(write_fd)
                    write_fd = None
                    self.assertEqual(serve(lambda: supervisor, read_fd, output_fd), 1)
                    supervisor.stop.assert_called_once()
                    supervisor.tick.assert_not_called()
                    supervisor.start.assert_not_called()
                finally:
                    for fd in (read_fd, write_fd, reply_fd, output_fd):
                        if fd is not None:
                            os.close(fd)

    def prepare(self):
        backend = Backend()
        owner = OwnedEffectsSupervisor(backend_factory=lambda: backend)
        owner.context = dict(generation=7, pid=123, starttime="456",
                             instance="abcdef_123_456",
                             plugin_inventory=list(backend.actual_inventory))
        return owner, backend

    def test_acknowledged_generation_is_guarded_before_unload(self):
        owner, backend = self.prepare()
        owner._fallback()
        self.assertEqual(backend.commands, ["status", "guard 7 off"])
        self.assertTrue(backend.unloaded)

    def test_replacement_inventory_is_never_touched(self):
        owner, backend = self.prepare()
        backend.actual_inventory = [{"name": "weather", "handle": "replacement"}]
        with self.assertRaisesRegex(RuntimeError, "ownership changed"):
            owner._fallback()
        self.assertEqual(backend.commands, [])
        self.assertFalse(backend.unloaded)

    def test_replacement_generation_is_never_stopped_or_unloaded(self):
        owner, backend = self.prepare()
        backend.generation = 8
        with self.assertRaisesRegex(RuntimeError, "generation changed"):
            owner._fallback()
        self.assertEqual(backend.commands, ["status"])
        self.assertFalse(backend.unloaded)

    def test_missing_acknowledgement_refuses_native_recovery(self):
        owner, backend = self.prepare()
        owner.context = None
        with self.assertRaisesRegex(RuntimeError, "before ownership acknowledgement"):
            owner._fallback()
        self.assertEqual(backend.commands, [])
        self.assertFalse(backend.unloaded)

    def test_crash_preserves_error_even_after_successful_native_fallback(self):
        owner, backend = self.prepare()
        owner.process = SimpleNamespace(stdin=Mock())
        owner.pidfd = 123
        owner._begin_action()
        with patch("ui.effects_proxy.select.select", return_value=([123], [], [])), \
                patch.object(owner, "_reap") as reap:
            status = owner._failure("effects owner lost its reply")
        reap.assert_called_once()
        self.assertTrue(backend.unloaded)
        self.assertEqual(status["state"], "cleanup_failed")
        self.assertIn("lost its reply", status["error"])

    def test_ambiguous_replies_cannot_mutate_ownership(self):
        replies = [
            b'{"id":1,"id":1,"ok":true,"status":{"state":"stopped"}}',
            b'{"id":true,"ok":true,"status":{"state":"stopped"}}',
            b'{"id":1,"ok":true,"status":{"value":NaN}}',
            b'{"id":1,"ok":true,"status":{"value":' + b'[' * 17 + b'0' + b']' * 17 + b'}}',
        ]
        for reply in replies:
            with self.subTest(reply=reply):
                owner, _ = self.prepare()
                original = owner.context.copy()
                owner.process = SimpleNamespace(stdin=Mock())
                owner.buffer = bytearray(reply + b"\n")
                owner._begin_action()
                with patch("ui.effects_proxy.select.select", return_value=([], [owner.process.stdin], [])), \
                        patch("ui.effects_proxy.os.write", side_effect=lambda fd, data: len(data)):
                    with self.assertRaises((ValueError, RuntimeError)):
                        owner._rpc("status")
                self.assertEqual(owner.context, original)
                self.assertFalse(owner.known_stopped)


if __name__ == "__main__":
    unittest.main()
