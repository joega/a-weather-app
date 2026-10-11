package locale

import "testing"

func TestMatching(t *testing.T) {
	for tag, want := range map[string]string{"hi-IN": "hi", "zh-Latn-TW": "", "en-IN": "en", "zh-Hant": "zh-Hant", "zh_TW.UTF-8": "zh-Hant", "zh-HK": "zh-Hant", "zh-MO": "zh-Hant", "zh-Hant-CN": "zh-Hant", "zh-Hans-TW": "zh-Hans", "zh-CN": "zh-Hans", "ru-RU": "uk", "ar-SA": "", "pl_PL.UTF-8": "pl"} {
		if got := Match(tag); got != want {
			t.Errorf("%s: %s want %s", tag, got, want)
		}
	}
	if got := Detect([]string{"ar-SA", "hi-IN", "en-US"}); got != "hi" {
		t.Fatal(got)
	}
	if got := Detect([]string{"ar-SA", "zz"}); got != "en" {
		t.Fatal(got)
	}
	if ProviderLanguage("zh-Hant") == "zh" {
		t.Fatal("Traditional Chinese requested as Simplified")
	}
}
func TestSystem(t *testing.T) {
	t.Setenv("LANGUAGE", "ar:hi_IN:en_IN")
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if System() != "hi" {
		t.Fatal(System())
	}
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "zh_TW.UTF-8")
	if System() != "zh-Hant" {
		t.Fatal(System())
	}
}
