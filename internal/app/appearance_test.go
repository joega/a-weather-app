package app

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
)

func TestAppearancePersistenceValidationAndIsolation(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	a.Snapshot() // Settle startup completions before comparing the existing timer.
	if doc, err := a.state.Read(appearanceFile, appearanceBytes); err != nil || doc != nil {
		t.Fatal("opening wrote defaults", doc, err)
	}
	controls, dashboard := safeio.Clone(a.controls), a.dashboardSnapshot()
	interval := a.Interval()
	for _, scale := range []float64{1.25, 1.5, 1} {
		reply, _ := a.Handle(context.Background(), request("set_appearance", M{"appearance": M{"text_scale": scale}}))
		if reply["ok"] != true || a.appearanceSnapshot()["text_scale"] != scale {
			t.Fatal(reply)
		}
	}
	if code := a.setAppearance(M{"text_scale": 1.5, "high_contrast": true}); code != "" {
		t.Fatal(code)
	}
	before := a.appearanceSnapshot()
	for _, patch := range []M{nil, {}, {"text_scale": 0.0}, {"text_scale": 2.0}, {"text_scale": math.NaN()}, {"text_scale": math.Inf(1)}, {"text_scale": "1.25"}, {"text_scale": true}, {"high_contrast": 1.0}, {"unknown": false}, {"schema_version": 1.0}} {
		if code := a.setAppearance(patch); code != "invalid_request" || !reflect.DeepEqual(a.appearanceSnapshot(), before) {
			t.Fatal("invalid patch changed preferences", patch, code)
		}
	}
	if !reflect.DeepEqual(controls, a.controls) || !reflect.DeepEqual(dashboard, a.dashboardSnapshot()) || a.Interval() != interval || a.radar != nil || a.precipitation != nil || a.airOutlook != nil {
		t.Fatalf("readability isolation: controls=%v dashboard=%v interval=%v/%v radar=%v precipitation=%v air=%v", reflect.DeepEqual(controls, a.controls), reflect.DeepEqual(dashboard, a.dashboardSnapshot()), interval, a.Interval(), a.radar != nil, a.precipitation != nil, a.airOutlook != nil)
	}
	// Older readers keep their existing document unchanged on rollback.
	if saved, err := readControls(a.state, a.country); err != nil || !reflect.DeepEqual(saved, controls) {
		t.Fatal("controls compatibility lost", saved, err)
	}
	reopened, err := New(a.state, Options{Offline: true, Now: a.options.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if !reflect.DeepEqual(reopened.appearanceSnapshot(), before) {
		t.Fatal("readability did not survive restart")
	}
	before["text_scale"] = 1.0
	if a.appearanceSnapshot()["text_scale"] != 1.5 {
		t.Fatal("snapshot aliased preferences")
	}
	if raw, err := ipc.Encode(a.appearanceSnapshot(), appearanceBytes); err != nil || len(raw) > 128 {
		t.Fatal("appearance envelope exceeded 128 bytes", len(raw), err)
	}
}

func TestAppearanceDamagedStateAndDurability(t *testing.T) {
	state := testState(t)
	if err := state.Write(appearanceFile, M{"schema_version": 99.0}, appearanceBytes); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Offline: true})
	if err != nil {
		t.Fatal("optional readability prevented startup", err)
	}
	defer a.Close(context.Background())
	if a.appearance.errorCode != "state_unavailable" || !reflect.DeepEqual(a.appearance.preferences, defaultAppearance()) {
		t.Fatal(a.appearanceSnapshot())
	}
	faults := &savedFaultFiles{Directory: state, fail: appearanceFile}
	a.appearance.files = faults
	if code := a.setAppearance(M{"text_scale": 1.5}); code != "state_io_failed" || a.appearance.preferences["text_scale"] != 1.0 {
		t.Fatal("failed rename applied preferences", code)
	}
	faults.after = true
	if code := a.setAppearance(M{"text_scale": 1.5}); code != "save_unconfirmed" || a.appearanceSnapshot()["error"] != "save_unconfirmed" || a.appearance.preferences["text_scale"] != 1.5 {
		t.Fatal("unconfirmed rename lost or falsely confirmed", code)
	}
	faults.fail = ""
	if code := a.setAppearance(M{"text_scale": 1.5}); code != "" || a.appearanceSnapshot()["error"] != nil {
		t.Fatal("retry failed", code)
	}
	writes := len(faults.writes)
	if code := a.setAppearance(M{"text_scale": 1.5}); code != "" || len(faults.writes) != writes {
		t.Fatal("unchanged preferences wrote again", code)
	}
	if code := a.setAppearance(M{"high_contrast": true}); code != "" || a.appearance.preferences["text_scale"] != 1.5 {
		t.Fatal("independent patch reset text size", code)
	}
}
