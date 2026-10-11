package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

func TestLanguagePersistenceAutomaticAndState(t *testing.T) {
	t.Setenv("LANGUAGE", "hi-IN")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	a := newTestApp(t, Options{Offline: true})
	before := a.Snapshot()
	for _, choice := range []string{"en", "zh-Hant", "ru", "auto"} {
		reply, _ := a.Handle(context.Background(), request("set_language", M{"language": M{"choice": choice}}))
		if reply["ok"] != true {
			t.Fatal(reply)
		}
		expected := choice
		if choice == "ru" {
			expected = "uk"
		}
		if choice == "auto" {
			expected = "hi"
		}
		snap := a.Snapshot()
		if object(snap["language"])["effective"] != expected {
			t.Fatal(snap["language"])
		}
		for _, key := range []string{"location", "current", "hourly", "daily", "source"} {
			if !reflect.DeepEqual(before[key], snap[key]) {
				t.Fatal("language changed canonical weather", key)
			}
		}
		saved, err := readControls(a.state, a.country)
		if err != nil || saved["language"] != a.controls["language"] {
			t.Fatal(saved, err)
		}
	}
	controls := safeio.Clone(a.controls)
	for _, v := range []M{{"choice": "xx"}, {"choice": "auto", "effective": "zh"}, {"choice": "en", "effective": "hi"}, {"choice": "en", "unknown": true}} {
		reply, _ := a.Handle(context.Background(), request("set_language", M{"language": v}))
		if reply["ok"] != false {
			t.Fatal("accepted", v)
		}
		if !reflect.DeepEqual(controls, a.controls) {
			t.Fatal("invalid choice mutated controls")
		}
	}
}
func TestLanguageSwitchRejectsPendingSearch(t *testing.T) {
	started := make(chan M, 2)
	release := make(chan struct{})
	a := newTestApp(t, Options{SearchPlaces: func(ctx context.Context, q M) ([]any, error) {
		started <- q
		select {
		case <-release:
		case <-ctx.Done():
		}
		return []any{cityRow(1)}, nil
	}})
	a.Handle(context.Background(), searchRequest("Berlin"))
	first := <-started
	generation := a.search.generation
	reply, _ := a.Handle(context.Background(), request("set_language", M{"language": M{"choice": "zh-Hant"}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	if a.search.generation == generation || a.search.status != "idle" {
		t.Fatal("search not invalidated")
	}
	a.Handle(context.Background(), searchRequest("Berlin"))
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.Snapshot()
		select {
		case q := <-started:
			if q["language"] != "zh-Hant" || first["language"] == q["language"] {
				t.Fatal(first, q)
			}
			readySearch(t, a)
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("replacement search did not start")
}

func TestLanguageCatalogFallbackRetainsChoice(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	reply, _ := a.Handle(context.Background(), request("set_language", M{"language": M{"choice": "hi", "effective": "en"}}))
	if reply["ok"] != true || a.controls["language"] != "hi" || a.effectiveLanguage() != "en" {
		t.Fatal(reply, a.controls, a.effectiveLanguage())
	}
	saved, err := readControls(a.state, a.country)
	if err != nil || saved["language"] != "hi" {
		t.Fatal("fallback lost preference", saved, err)
	}
}
