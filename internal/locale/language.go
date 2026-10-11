// Package locale matches interface languages without changing regional formatting.
package locale

import (
	"context"
	"os"
	"strings"
)

var supported = map[string]bool{"en": true, "es": true, "fr": true, "de": true, "pt": true, "ja": true, "zh-Hans": true, "zh-Hant": true, "uk": true, "hi": true, "it": true, "ko": true, "id": true, "pl": true}

func Valid(code string) bool { return supported[code] }
func Choice(code string) (string, bool) {
	if code == "ru" {
		code = "uk"
	}
	return code, code == "auto" || Valid(code)
}

// Match accepts POSIX locales and BCP 47 tags. Script takes precedence over region.
func Match(tag string) string {
	tag = strings.TrimSpace(strings.Split(strings.Split(tag, ".")[0], "@")[0])
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(tag, "_", "-")), "-")
	if len(parts) == 0 {
		return ""
	}
	base := parts[0]
	if base == "ru" {
		return "uk"
	}
	if base == "zh" {
		for _, part := range parts[1:] {
			if part == "hant" {
				return "zh-Hant"
			}
			if part == "hans" {
				return "zh-Hans"
			}
		}
		for _, part := range parts[1:] {
			if len(part) == 4 {
				return ""
			}
		}
		for _, part := range parts[1:] {
			if part == "tw" || part == "hk" || part == "mo" {
				return "zh-Hant"
			}
		}
		return "zh-Hans"
	}
	if Valid(base) {
		return base
	}
	return ""
}

func Detect(preferences []string) string {
	for _, tag := range preferences {
		if code := Match(tag); code != "" {
			return code
		}
	}
	return "en"
}

func System() string {
	preferences := []string{}
	// LANGUAGE is a preference list; LC_ALL overrides the process locale category.
	if list := os.Getenv("LANGUAGE"); list != "" {
		preferences = append(preferences, strings.Split(list, ":")...)
	}
	if tag := os.Getenv("LC_ALL"); tag != "" {
		preferences = append(preferences, tag)
	} else if tag := os.Getenv("LC_MESSAGES"); tag != "" {
		preferences = append(preferences, tag)
	}
	preferences = append(preferences, os.Getenv("LANG"))
	return Detect(preferences)
}

// ProviderLanguage avoids asking the provider for generic Chinese when the
// interface uses Traditional Chinese. Unavailable provider translations fall
// back to original place names under the provider's documented behavior.
func ProviderLanguage(code string) string {
	if code == "zh-Hans" {
		return "zh"
	}
	if Valid(code) {
		return strings.ToLower(code)
	}
	return "en"
}

type languageContextKey struct{}

func WithLanguage(ctx context.Context, code string) context.Context {
	if !Valid(code) {
		code = "en"
	}
	return context.WithValue(ctx, languageContextKey{}, code)
}
func ContextLanguage(ctx context.Context) string {
	code, _ := ctx.Value(languageContextKey{}).(string)
	if !Valid(code) {
		return "en"
	}
	return code
}
