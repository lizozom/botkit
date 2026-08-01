package identity

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"050-000-0001":      "972500000001",
		"+972500000001":     "972500000001",
		"972500000001":      "972500000001",
		"0500000001":        "972500000001",
		"":                  "",
		"   ":               "",
		"abc":               "",
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
	if got := Normalize("0500000001"); got != "1500000001" {
		t.Errorf("Normalize with CC=1 = %q, want 1500000001", got)
	}
}
