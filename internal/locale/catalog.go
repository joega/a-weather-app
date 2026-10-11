package locale

import (
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed catalogs/*.json
var catalogFiles embed.FS
var catalogs sync.Map

func catalog(code string) map[string]json.RawMessage {
	if value, ok := catalogs.Load(code); ok {
		return value.(map[string]json.RawMessage)
	}
	result := map[string]json.RawMessage{}
	if Valid(code) {
		if raw, err := catalogFiles.ReadFile("catalogs/" + code + ".json"); err == nil {
			_ = json.Unmarshal(raw, &result)
		}
	}
	actual, _ := catalogs.LoadOrStore(code, result)
	return actual.(map[string]json.RawMessage)
}
func sourceText(code, source string, n int) string {
	value := catalog(code)[source]
	var text string
	if json.Unmarshal(value, &text) == nil && text != "" {
		return text
	}
	var forms []string
	if json.Unmarshal(value, &forms) == nil {
		index := pluralIndex(code, n)
		if index < len(forms) && forms[index] != "" {
			return forms[index]
		}
	}
	return source
}
func pluralIndex(code string, n int) int {
	switch code {
	case "zh-Hans", "zh-Hant", "ja", "ko", "id":
		return 0
	case "uk":
		if n%10 == 1 && n%100 != 11 {
			return 0
		}
		if n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14) {
			return 1
		}
		return 2
	case "pl":
		if n == 1 {
			return 0
		}
		if n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14) {
			return 1
		}
		return 2
	case "fr", "pt", "hi":
		if n <= 1 {
			return 0
		}
		return 1
	default:
		if n == 1 {
			return 0
		}
		return 1
	}
}
func T(code, source string, args ...any) string {
	text := sourceText(code, source, -1)
	// Replace longest placeholders first, without interpreting provider text as templates.
	pairs := []string{}
	for i := len(args); i > 0; i-- {
		pairs = append(pairs, "%"+strconv.Itoa(i), fmt.Sprint(args[i-1]))
	}
	if len(pairs) > 0 {
		text = strings.NewReplacer(pairs...).Replace(text)
	}
	return text
}
func N(code, source string, n int) string {
	text := sourceText(code, source, n)
	if text == source && source == "%n alert(s)" {
		if n == 1 {
			text = "%n alert"
		} else {
			text = "%n alerts"
		}
	}
	return strings.ReplaceAll(text, "%n", strconv.Itoa(n))
}

// DateTime uses unambiguous numeric dates and local 24-hour times for desktop
// notification text. The GUI formats canonical timestamps with QLocale instead.
func DateTime(t time.Time) string { return t.Format("2006-01-02 15:04 MST") }
