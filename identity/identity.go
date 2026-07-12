// Package identity canonicalizes phone numbers so WhatsApp identities and
// app-side lists (rosters, allowlists) compare equal.
//
// Phase 0 provides only the pure Normalize. The LID<->phone resolution and
// participant matching that need a live whatsmeow client land in Phase 1
// alongside the transport, which holds the client.
package identity

import "strings"

// DefaultCountryCode is prepended to local numbers that start with a national
// trunk "0". Defaults to Israel ("972"); an app may override it at startup.
var DefaultCountryCode = "972"

// Normalize returns a canonical phone key: digits only, no "+", with a national
// leading "0" replaced by DefaultCountryCode. Returns "" for empty/garbage.
//
//	"054-626-0906"   -> "972546260906"
//	"+972546260906"  -> "972546260906"
//	"972546260906"   -> "972546260906"
func Normalize(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if d == "" {
		return ""
	}
	if strings.HasPrefix(d, "0") {
		d = DefaultCountryCode + strings.TrimLeft(d, "0")
	}
	return d
}
