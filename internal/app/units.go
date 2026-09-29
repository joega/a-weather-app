package app

import "github.com/joega/a-weather-app/internal/safeio"

// Derive automatic units from the same saved country identity used by the bar.
// Unknown legacy countries retain the existing Fahrenheit default.
func controlsForCountry(controls M, country any) M {
	if controls["units_mode"] != "auto" {
		return controls
	}
	controls = safeio.Clone(controls)
	controls["units"] = "F"
	if country != nil && country != "US" {
		controls["units"] = "C"
	}
	return controls
}

func readControls(state *safeio.Directory, country any) (M, error) {
	controls := DefaultControls()
	saved, err := state.Read("controls.json", 8192)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		controls, err = PatchControls(controls, saved)
		if err != nil {
			return nil, err
		}
	}
	return controlsForCountry(controls, country), nil
}
