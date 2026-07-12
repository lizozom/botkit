package identity

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"054-626-0906":  "972546260906",
		"+972546260906": "972546260906",
		"972546260906":  "972546260906",
		"0546260906":    "972546260906",
		"":              "",
		"   ":           "",
		"abc":           "",
		"+1 (415) 555-2671": "14155552671",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCountryCodeOverride(t *testing.T) {
	orig := DefaultCountryCode
	defer func() { DefaultCountryCode = orig }()
	DefaultCountryCode = "1"
	if got := Normalize("0546260906"); got != "1546260906" {
		t.Errorf("Normalize with CC=1 = %q, want 1546260906", got)
	}
}
