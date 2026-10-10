package app

import (
	"errors"
	"reflect"

	"github.com/joega/a-weather-app/internal/safeio"
)

const appearanceFile = "appearance.json"
const appearanceBytes = 1024

type appearanceFiles interface {
	Read(string, int) (M, error)
	Write(string, any, int) error
}
type appearanceState struct {
	preferences M
	errorCode   string
	files       appearanceFiles
}

func defaultAppearance() M {
	return M{"schema_version": 1.0, "text_scale": 1.0, "high_contrast": false}
}
func validateAppearance(v M) error {
	if len(v) != 3 || v["schema_version"] != 1.0 || (v["text_scale"] != 1.0 && v["text_scale"] != 1.25 && v["text_scale"] != 1.5) {
		return errors.New("appearance preferences")
	}
	if _, ok := v["high_contrast"].(bool); !ok {
		return errors.New("appearance contrast")
	}
	return nil
}
func (a *App) initAppearance() {
	a.appearance = &appearanceState{preferences: defaultAppearance(), files: a.state}
	doc, err := a.state.Read(appearanceFile, appearanceBytes)
	if err != nil || doc != nil && validateAppearance(doc) != nil {
		a.appearance.errorCode = "state_unavailable"
		return
	}
	if doc != nil {
		a.appearance.preferences = doc
	}
}
func (a *App) appearanceSnapshot() M {
	v := defaultAppearance()
	var code any
	if a.appearance != nil {
		v = safeio.Clone(a.appearance.preferences)
		if a.appearance.errorCode != "" {
			code = a.appearance.errorCode
		}
	}
	v["error"] = code
	return v
}
func (a *App) setAppearance(patch M) string {
	if len(patch) < 1 || len(patch) > 2 {
		return "invalid_request"
	}
	if a.appearance == nil || a.appearance.files == nil {
		return "state_io_failed"
	}
	s := a.appearance
	next := safeio.Clone(s.preferences)
	for key, value := range patch {
		if key != "text_scale" && key != "high_contrast" {
			return "invalid_request"
		}
		next[key] = value
	}
	if validateAppearance(next) != nil {
		return "invalid_request"
	}
	if reflect.DeepEqual(next, s.preferences) && s.errorCode == "" {
		return ""
	}
	confirmed := s.files.Write(appearanceFile, next, appearanceBytes) == nil
	if !confirmed {
		// A failed directory fsync can follow a successful rename. Preserve
		// visible bytes while reporting that durability is not confirmed.
		actual, err := s.files.Read(appearanceFile, appearanceBytes)
		if err != nil || !reflect.DeepEqual(actual, next) {
			return "state_io_failed"
		}
	}
	s.preferences, s.errorCode = next, ""
	if !confirmed {
		s.errorCode = "save_unconfirmed"
		return "save_unconfirmed"
	}
	return ""
}
