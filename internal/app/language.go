package app

import (
	"errors"

	"github.com/joega/a-weather-app/internal/locale"
	"github.com/joega/a-weather-app/internal/safeio"
)

func (a *App) effectiveLanguage() string {
	if a.languageEffective != "" {
		return a.languageEffective
	}
	if code := stringOf(a.controls["language"]); code != "auto" && locale.Valid(code) {
		return code
	}
	return locale.System()
}

func (a *App) setLanguage(v M) error {
	if len(v) < 1 || len(v) > 2 {
		return errors.New("invalid language")
	}
	for key := range v {
		if key != "choice" && key != "effective" {
			return errors.New("invalid language field")
		}
	}
	choice, valid := locale.Choice(stringOf(v["choice"]))
	if !valid {
		return errors.New("invalid language choice")
	}
	effective := choice
	if choice == "auto" {
		effective = locale.System()
	}
	if supplied, exists := v["effective"]; exists {
		code, ok := supplied.(string)
		if !ok || !locale.Valid(code) || (choice != "auto" && code != choice && code != "en") {
			return errors.New("invalid effective language")
		}
		effective = code
	}
	controls := safeio.Clone(a.controls)
	controls["language"] = choice
	if err := a.state.Write("controls.json", controls, 8192); err != nil {
		return err
	}
	changed := a.effectiveLanguage() != effective
	a.controls, a.languageEffective = controls, effective
	if changed {
		a.refreshMetadata()
		a.cancelSearch()
	}
	a.syncNotificationLanguage()
	return nil
}

func (a *App) syncNotificationLanguage() {
	code := a.effectiveLanguage()
	if a.notifications != nil {
		a.notifications.SetLanguage(code)
	}
	if a.warnings != nil {
		a.warnings.SetLanguage(code)
	}
}
