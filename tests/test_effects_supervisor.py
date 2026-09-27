"""Supervisor protocol checks with an in-memory native backend, never a compositor."""
from copy import deepcopy
from datetime import datetime, timezone
from pathlib import Path
import unittest

from ui.bridge import DEFAULT_CONTROLS
from ui.effects import EffectsSupervisor
from weather.runtime import select_weather


class Backend:
    def __init__(self, effects):
        self.commands, self.published = [], []
        self.unloaded = False
        self.status = dict(enabled=True, session_generation=7, fps=30,
            reduced_motion=False, window_physics=True, accumulation=True,
            target_parameters={key: effects[key] for key in ("rain_intensity", "wind_x")},
            snow_target_parameters=dict(intensity=effects["snow_intensity"], temperature_known=False))

    def native(self, command):
        self.commands.append(command)
        if command == "guard 7 off":
            self.status["enabled"] = False
        return deepcopy(self.status)

    def children_alive(self):
        return True

    def publish(self, path, envelope):
        self.published.append((path, deepcopy(envelope)))

    def stop_children(self):
        return []

    def unload(self):
        self.unloaded = True


class SupervisorTests(unittest.TestCase):
    def prepare(self):
        selected = select_weather(None, now=datetime.now(timezone.utc))
        # An intentionally large unrelated forecast must never reach the sky file.
        selected["forecast"] = {"private": "x" * 100000}
        backend = Backend(selected["effects"])
        supervisor = EffectsSupervisor(backend, clock=lambda: 1)
        supervisor.state, supervisor.loaded = "running", True
        supervisor.generation, supervisor.deadline = 7, 30
        supervisor.directory = Path("/tmp/offline-effects-protocol")
        supervisor.host_fps = 30
        return supervisor, backend, selected

    def test_heartbeat_publishes_minimal_sky_and_avoids_redundant_updates(self):
        supervisor, backend, selected = self.prepare()
        self.assertEqual(supervisor.tick(selected, DEFAULT_CONTROLS)["state"], "running")
        envelope = backend.published[-1][1]
        self.assertEqual(set(envelope), {"schema_version", "selected_at", "effects"})
        self.assertEqual(envelope["effects"], selected["effects"])
        backend.commands.clear()
        self.assertEqual(supervisor.tick(selected, DEFAULT_CONTROLS)["state"], "running")
        self.assertEqual(backend.commands, ["status", "status"])

    def test_replaced_native_session_is_never_stopped_or_unloaded(self):
        supervisor, backend, selected = self.prepare()
        backend.status["session_generation"] = 8
        status = supervisor.tick(selected, DEFAULT_CONTROLS)
        self.assertEqual(status["state"], "cleanup_failed")
        self.assertFalse(backend.unloaded)
        self.assertTrue(supervisor.loaded)
        self.assertFalse(any(command.startswith("guard") for command in backend.commands))
        self.assertEqual(backend.published, [])


if __name__ == "__main__":
    unittest.main()
