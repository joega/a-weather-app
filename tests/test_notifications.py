"""Notification privacy and delivery-policy checks without desktop messages."""
from datetime import datetime, timedelta, timezone
import tempfile
import unittest
from unittest.mock import Mock, patch

from ui.bridge import StateDirectory
from ui.notifications import Notifier as DesktopNotifier, PrecipitationWatcher, text
from weather.provider import DEFAULT_LOCATION


class Notifier:
    available = True
    process = None

    def __init__(self):
        self.messages = []
        self.fail = False
        self.cancelled = 0

    def start(self, title, body):
        if self.fail:
            raise OSError("synthetic delivery failure")
        self.messages.append((title, body))

    def cancel(self):
        self.cancelled += 1

    def poll(self):
        return None

    def finish_close(self):
        pass


class NotificationTests(unittest.TestCase):
    def test_notification_text_and_arguments_are_not_markup_or_options(self):
        hostile = '--icon=evil <b>&test</b>\x00\n\u202e$(touch /tmp/not-executed)'
        cleaned = text(hostile, 320)
        for character in "<>&\x00\n\u202e":
            self.assertNotIn(character, cleaned)
        process = Mock(pid=12345)
        with patch("ui.notifications.subprocess.Popen", return_value=process) as launch, \
                patch("ui.notifications.os.pidfd_open", return_value=99):
            notifier = DesktopNotifier()
            notifier.start(hostile, hostile)
        arguments, options = launch.call_args
        command = arguments[0]
        separator = command.index("--")
        self.assertEqual(command[separator + 1:], [text(hostile, 80), cleaned])
        self.assertFalse(options.get("shell", False))
        self.assertEqual(command[0], "/usr/bin/notify-send")

    def setUp(self):
        self.folder = tempfile.TemporaryDirectory()
        self.state = StateDirectory(self.folder.name)
        self.now = datetime(2026, 9, 27, 16, tzinfo=timezone.utc)
        self.notifier = Notifier()
        self.watcher = PrecipitationWatcher(self.state, now=lambda: self.now,
                                            notifier=self.notifier)
        self.forecast = dict(location=DEFAULT_LOCATION,
            fetched_at=self.now.isoformat(), hourly=[dict(
                time=(self.now + timedelta(hours=1)).isoformat(), precipitation_probability=1)])

    def tearDown(self):
        self.watcher.begin_close()
        self.watcher.finish_close()
        self.state.close()
        self.folder.cleanup()

    def test_disabled_is_silent_and_does_not_create_history(self):
        self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(self.notifier.messages, [])
        self.assertIsNone(self.state.read("notifications.json", 32768))

    def test_stale_forecast_and_quiet_hours_suppress_delivery(self):
        self.watcher.configure({"enabled": True})
        self.forecast["fetched_at"] = (self.now - timedelta(hours=2)).isoformat()
        self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(self.watcher.mode, "waiting")
        self.forecast["fetched_at"] = self.now.isoformat()
        self.watcher.configure({"quiet_start": 11, "quiet_end": 13})
        self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(self.watcher.mode, "quiet")
        self.assertEqual(self.notifier.messages, [])

    def test_repeated_event_delivers_once_without_body_history(self):
        self.watcher.configure({"enabled": True, "quiet_enabled": False})
        for _ in range(5):
            self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(len(self.notifier.messages), 1)
        events = self.state.read("notifications.json", 32768)["events"]
        self.assertEqual(len(events), 1)
        self.assertEqual(set(events[0]), {"location", "start", "end", "reserved", "expires"})

    def test_uncertain_delivery_reservation_survives_restart(self):
        self.watcher.configure({"enabled": True, "quiet_enabled": False})
        self.notifier.fail = True
        self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(self.watcher.delivery, "failed")
        self.watcher.begin_close()
        self.watcher.finish_close()
        self.notifier.fail = False
        self.watcher = PrecipitationWatcher(self.state, now=lambda: self.now,
                                            notifier=self.notifier)
        self.watcher.tick(self.forecast, DEFAULT_LOCATION)
        self.assertEqual(self.notifier.messages, [])
