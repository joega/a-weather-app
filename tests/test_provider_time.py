from datetime import datetime, timezone
import unittest
from zoneinfo import ZoneInfo

from weather.provider import CURRENT, DAILY, HOURLY, ForecastError, _number, parse_forecast


class ProviderTimeTests(unittest.TestCase):
    def payload(self, zone, times):
        row = dict(temperature_2m=15, apparent_temperature=14,
            relative_humidity_2m=50, cloud_cover=20, precipitation=0,
            weather_code=0, wind_speed_10m=1, wind_direction_10m=180,
            wind_gusts_10m=2, is_day=1)
        daily = dict(temperature_2m_max=20, temperature_2m_min=10,
            sunrise=None, sunset=None, precipitation_probability_max=0, weather_code=0)
        return dict(timezone=zone,
            current_units=dict(CURRENT, time="unixtime", interval="seconds"),
            hourly_units=dict(HOURLY, time="unixtime"),
            daily_units=dict(DAILY, time="unixtime"),
            current=dict(row, time=times[0], interval=900),
            hourly=dict(time=times, **{key: [value] * len(times) for key, value in
                dict(row, precipitation_probability=0, visibility=10000).items()}),
            daily=dict(time=[times[0]], **{key: [value] for key, value in daily.items()}))

    def test_local_calendar_dates_and_dst_hour_order(self):
        for zone, date in (("America/New_York", "2026-11-01"),
                           ("America/New_York", "2026-03-08"),
                           ("Asia/Kathmandu", "2026-09-27"),
                           ("Pacific/Auckland", "2026-09-27")):
            with self.subTest(zone=zone, date=date):
                start = datetime.fromisoformat(date).replace(tzinfo=ZoneInfo(zone)).timestamp()
                times = [start + index * 3600 for index in range(25)]
                location = dict(name="Test", latitude=0, longitude=0, timezone=zone)
                result = parse_forecast(self.payload(zone, times), location,
                    datetime.fromtimestamp(start, timezone.utc))
                self.assertEqual(result["daily"][0]["date"], date)
                normalized = [datetime.fromisoformat(row["time"].replace("Z", "+00:00"))
                    for row in result["hourly"]]
                self.assertTrue(all((right - left).total_seconds() == 3600
                    for left, right in zip(normalized, normalized[1:])))

    def test_huge_integer_is_provider_validation_error(self):
        with self.assertRaises(ForecastError):
            _number(10 ** 1000, "temperature")
