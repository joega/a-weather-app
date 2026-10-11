package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/locale"
	"github.com/joega/a-weather-app/internal/safeio"
)

func metadataApp(t *testing.T, resolve func(context.Context, M) (M, error)) *App {
	t.Helper()
	t.Setenv("LANGUAGE", "en")
	state := testState(t)
	profile := savedFixture(0)
	if _, err := createSavedLocations(state, profile); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Now: func() time.Time { return savedRuntimeNow }, ResolveSelection: resolve, FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		t.Error("metadata requested weather")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
	return a
}
func languageRequest(code string) M { return request("set_language", M{"language": M{"choice": code}}) }
func waitMetadata(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.Snapshot()
		if !a.metadata.running && a.metadata.pending == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("metadata request did not settle")
}
func metadataRecord(profile M, name string) M {
	location := safeio.Clone(object(profile["location"]))
	location["name"] = name
	return M{"location": location, "country_code": profile["country_code"], "place": safeio.Clone(object(profile["place"]))}
}
func TestLocalizedMetadataLatestLanguagePreservesWeather(t *testing.T) {
	type job struct {
		language string
		ctx      context.Context
		reply    chan M
	}
	jobs := make(chan job, 4)
	a := metadataApp(t, func(ctx context.Context, selection M) (M, error) {
		if selection["place_id"] != 100.0 {
			t.Error("lost provider identity", selection)
		}
		j := job{locale.ContextLanguage(ctx), ctx, make(chan M, 1)}
		jobs <- j
		return <-j.reply, nil
	})
	before := safeio.Clone(a.profile)
	controls := safeio.Clone(a.controls)
	nextFetch := a.nextFetch
	generation := a.forecastGeneration
	reply, _ := a.Handle(context.Background(), languageRequest("hi"))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	first := <-jobs
	if first.language != "hi" {
		t.Fatal(first.language)
	}
	a.Handle(context.Background(), languageRequest("uk"))
	if first.ctx.Err() == nil {
		t.Fatal("old metadata not canceled")
	}
	select {
	case <-jobs:
		t.Fatal("replacement overlaps old metadata")
	default:
	}
	first.reply <- metadataRecord(before, "पुराना नाम")
	deadline := time.Now().Add(time.Second)
	var latest job
	for time.Now().Before(deadline) {
		a.Snapshot()
		select {
		case latest = <-jobs:
			goto started
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("latest metadata not started")
started:
	if latest.language != "uk" || a.location["name"] != object(before["location"])["name"] {
		t.Fatal("stale metadata published", latest.language, a.location)
	}
	latest.reply <- metadataRecord(before, "Локалізоване місто")
	waitMetadata(t, a)
	if a.location["name"] != "Локалізоване місто" {
		t.Fatal(a.location)
	}
	want := safeio.Clone(before)
	object(want["location"])["name"] = "Локалізоване місто"
	object(object(want["forecast"])["location"])["name"] = "Локалізоване місто"
	if !reflect.DeepEqual(a.profile, want) || a.nextFetch != nextFetch || a.forecastGeneration != generation {
		t.Fatal("metadata altered weather/freshness/scheduling")
	}
	controls["language"] = "uk"
	if !reflect.DeepEqual(a.controls, controls) {
		t.Fatal("metadata altered units/controls")
	}
	persisted, err := a.saved.profile(a.id)
	if err != nil || !reflect.DeepEqual(persisted, a.profile) {
		t.Fatal("label not durably saved", err)
	}
	// A previously issued weather completion must retain the new label.
	originalForecast := safeio.Clone(object(before["forecast"]))
	a.applyForecastCompletion(completion{point: a.forecastPoint, generation: a.generation, location: object(before["location"]), country: a.country, forecast: originalForecast})
	if a.location["name"] != "Локалізоване місто" || object(a.forecast["location"])["name"] != "Локалізоване місто" {
		t.Fatal("old forecast reset label")
	}
}
func TestLocalizedMetadataRejectsIdentityChanges(t *testing.T) {
	for _, field := range []string{"latitude", "longitude", "timezone", "country_code", "id"} {
		t.Run(field, func(t *testing.T) {
			profile := savedFixture(0)
			a := metadataApp(t, func(context.Context, M) (M, error) {
				row := metadataRecord(profile, "Wrong city")
				switch field {
				case "country_code":
					row[field] = "IN"
				case "id":
					object(row["place"])["id"] = 999.0
				case "timezone":
					object(row["location"])[field] = "UTC"
				default:
					object(row["location"])[field] = 1.0
				}
				return row, nil
			})
			before := safeio.Clone(a.profile)
			a.Handle(context.Background(), languageRequest("hi"))
			waitMetadata(t, a)
			if !reflect.DeepEqual(before, a.profile) {
				t.Fatal("untrusted identity altered city", field)
			}
		})
	}
}
func TestOfflineMetadataKeepsLastKnownLabel(t *testing.T) {
	a := metadataApp(t, func(context.Context, M) (M, error) { t.Error("offline metadata network"); return nil, nil })
	a.options.Offline = true
	before := safeio.Clone(a.profile)
	a.Handle(context.Background(), languageRequest("hi"))
	if a.metadata.running || a.metadata.pending != nil || !reflect.DeepEqual(before, a.profile) {
		t.Fatal("offline label/weather changed")
	}
}
