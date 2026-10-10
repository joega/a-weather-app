package app

import (
	"context"
	"strings"
	"testing"
)

type hintedEffects struct {
	fakeEffects
	hints, outputs []string
}

func (f *hintedEffects) CheckOutput(ctx context.Context, hint string) M {
	f.hints = append(f.hints, hint)
	return f.Check(ctx)
}
func (f *hintedEffects) SelectOutput(_ context.Context, output string) (M, error) {
	f.outputs = append(f.outputs, output)
	return f.SetupSnapshot(), nil
}

func TestEffectsCommandsPassAppMonitorHint(t *testing.T) {
	for _, op := range []string{"check_effects", "select_output", "start_effects", "start_live_effects"} {
		t.Run(op, func(t *testing.T) {
			fx := &hintedEffects{}
			a := newTestApp(t, Options{Offline: true, Effects: fx})
			args := M{"monitor_hint": "DP-1"}
			if op == "select_output" {
				args["output"] = ""
			}
			if op == "start_effects" {
				args["duration"] = 1.0
			}
			reply, _ := a.Handle(context.Background(), request(op, args))
			if reply["ok"] != true || len(fx.hints) != 1 || fx.hints[0] != "DP-1" {
				t.Fatal("monitor hint did not reach compatibility check", reply, fx.hints)
			}
			if op == "select_output" && (len(fx.outputs) != 1 || fx.outputs[0] != "") {
				t.Fatal("Automatic reset was not submitted", fx.outputs)
			}
		})
	}
}

func TestInvalidMonitorHintsHaveNoEffectsSideEffects(t *testing.T) {
	for _, hint := range []any{true, 1.0, nil, M{}, "DP-1\n", "../DP-1", strings.Repeat("D", 129)} {
		fx := &hintedEffects{}
		a := newTestApp(t, Options{Offline: true, Effects: fx})
		reply, _ := a.Handle(context.Background(), request("start_live_effects", M{"monitor_hint": hint}))
		if reply["ok"] != false || reply["error"] != "invalid_request" || len(fx.hints) != 0 || fx.starts != 0 {
			t.Fatal("invalid hint admitted effects", hint, reply)
		}
	}
	fx := &hintedEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	for _, args := range []M{{"output": nil}, {"output": true}, {"output": "DP-1\n"}} {
		reply, _ := a.Handle(context.Background(), request("select_output", args))
		if reply["ok"] != false || len(fx.outputs) != 0 {
			t.Fatal("invalid explicit selection reset the output", reply)
		}
	}
	reply, _ := a.Handle(context.Background(), request("stop_effects", M{"monitor_hint": "DP-1"}))
	if reply["error"] != "invalid_request" || fx.stops != 0 {
		t.Fatal("unexpected hint accepted on stop", reply)
	}
}
