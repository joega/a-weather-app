package app

import (
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
	"math"
	"strings"
	"time"
)

var friendlyConditions = map[string]string{"clear": "Clear", "partly_cloudy": "Partly cloudy", "cloudy": "Cloudy", "fog": "Fog", "drizzle": "Drizzle", "rain": "Rain", "snow": "Snow", "sleet": "Sleet", "thunderstorm": "Thunderstorm", "unknown": "Unavailable"}

func plainText(v any, n int) string { p, _ := weather.Plain(v, n, false); return stringOf(p) }

// Bar reads saved observations only and never initializes the app or any worker.
func Bar(state *safeio.Directory, now time.Time) M {
	r := M{"schema_version": 1.0, "label": "--° · Unavailable", "tooltip": "Weather and alerts unavailable", "freshness": "unavailable"}
	if state == nil {
		return r
	}
	if now.IsZero() {
		now = time.Now()
	}
	location, forecast, profile, mode, _, e := readSaved(state)
	country, _ := profileIdentity(profile, mode)
	if location != nil {
		r["tooltip"] = plainText(plainText(location["name"], 244)+" · Weather and alerts unavailable", 256)
		if country != nil && country != "US" {
			r["tooltip"] = plainText(plainText(location["name"], 244)+" · Weather unavailable · Alerts not supported here", 256)
		}
	}
	if e != nil || forecast == nil {
		return r
	}
	controls, e := readControls(state, country)
	if e != nil {
		return r
	}
	selected := weather.Select(forecast, now, "live", nil, "subtle", false, false)
	fresh := stringOf(selected["freshness"])
	r["freshness"] = fresh
	if fresh != "fresh" && fresh != "stale" {
		return r
	}
	current, e := weather.WeatherRecord(object(forecast["current"]))
	if e != nil {
		return r
	}
	degrees := "--"
	if t, ok := current["temperature_c"].(float64); ok {
		if controls["units"] == "F" {
			t = t*9/5 + 32
		}
		// Match Forecast.temp's JavaScript Math.round, including negative ties.
		degrees = fmt.Sprintf("%.0f", math.Floor(t+0.5))
	}
	condition := friendlyConditions[stringOf(current["condition"])]
	label := degrees + "° · " + condition
	if fresh == "stale" {
		label += " · Stale"
	}
	alerts := object(object(selected["forecast"])["alerts"])
	items, _ := alerts["items"].([]any)
	alertText := "Alerts unavailable"
	if country != nil && country != "US" {
		alertText = "Alerts not supported here"
	}
	if country == "US" && alerts["status"] == "available" {
		if len(items) > 0 {
			plural := ""
			if len(items) != 1 {
				plural = "s"
			}
			label += fmt.Sprintf(" · %d alert%s", len(items), plural)
		}
		events := []string{}
		for _, v := range items {
			event := plainText(object(v)["event"], 80)
			if event == "" {
				event = "Weather alert"
			}
			events = append(events, event)
			if len(events) == 2 {
				break
			}
		}
		alertText = strings.Join(events, "; ")
		if alertText == "" {
			alertText = "No active alerts in cached feed"
		}
	}
	r["label"] = plainText(label, 96)
	r["tooltip"] = plainText(plainText(location["name"], 244)+" · "+condition+" · "+alertText, 256)
	return r
}
