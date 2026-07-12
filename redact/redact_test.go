package redact

import (
	"strings"
	"testing"
)

func TestPhone(t *testing.T) {
	if got := Phone(""); got != "<empty>" {
		t.Errorf("Phone(\"\") = %q, want <empty>", got)
	}
	got := Phone("972546260906")
	if !strings.HasPrefix(got, "ph:") {
		t.Errorf("Phone() = %q, want ph: prefix", got)
	}
	if got != Phone("972546260906") {
		t.Error("Phone() not deterministic")
	}
	if len(got) != len("ph:")+8 {
		t.Errorf("Phone() = %q, want 8 hex chars after prefix", got)
	}
	if Phone("a") == Phone("b") {
		t.Error("Phone() collided on distinct inputs")
	}
}

func TestJID(t *testing.T) {
	if got := JID(""); got != "<empty>" {
		t.Errorf("JID(\"\") = %q, want <empty>", got)
	}
	got := JID("120363@g.us")
	if !strings.HasPrefix(got, "g:") {
		t.Errorf("JID() = %q, want g: prefix", got)
	}
	if got != JID("120363@g.us") {
		t.Error("JID() not deterministic")
	}
	// Phone and JID share the hash body and differ only by prefix — so the
	// same raw value is distinguishable by namespace in logs.
	if Phone("x")[3:] != JID("x")[2:] {
		t.Error("expected Phone and JID to share the hash body")
	}
}

func TestName(t *testing.T) {
	cases := map[string]string{
		"":           "<empty>",
		"Liza":       "Liza",
		"Liza Katz":  "Liza",
		"Liza\tKatz": "Liza",
	}
	for in, want := range cases {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}
