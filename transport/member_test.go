package transport

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func jid(user, server string) types.JID { return types.JID{User: user, Server: server} }

// sameJID is the identity rule behind both self-detection and the webauth
// membership check. The namespace-collision cases are the ones that matter: a
// LID and a phone number are both numeric, so a User-only comparison would let
// a stranger authenticate as a member whose phone happens to equal their LID.
func TestSameJID(t *testing.T) {
	cases := []struct {
		name string
		a, b types.JID
		want bool
	}{
		{"identical phone JIDs", jid("972500000001", types.DefaultUserServer), jid("972500000001", types.DefaultUserServer), true},
		{"identical LIDs", jid("11223344", types.HiddenUserServer), jid("11223344", types.HiddenUserServer), true},
		{"same digits, LID vs phone", jid("972500000001", types.HiddenUserServer), jid("972500000001", types.DefaultUserServer), false},
		{"same digits, phone vs LID", jid("972500000001", types.DefaultUserServer), jid("972500000001", types.HiddenUserServer), false},
		{"different users, same server", jid("972500000001", types.DefaultUserServer), jid("972500000000", types.DefaultUserServer), false},
		{"empty vs empty", types.JID{}, types.JID{}, false},
		{"empty vs real", types.JID{}, jid("972500000001", types.DefaultUserServer), false},
		{"real vs empty", jid("972500000001", types.DefaultUserServer), types.JID{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameJID(tc.a, tc.b); got != tc.want {
				t.Errorf("sameJID(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
