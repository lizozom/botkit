// Package redact provides one-way short hashes for PII in incidental/debug
// log lines.
//
// Logs typically go to a shared aggregator, so phones and JIDs — stable
// identifiers — are replaced with short SHA-256 prefixes. The prefix is still
// useful for correlating related events on a single line ("this hash got
// messaged then replied") without exposing the underlying personal data.
//
// Raw values may still be intentional in a dedicated audit/event channel; that
// is the app's call. Use redact.* only for incidental debug lines.
package redact

import (
	"crypto/sha256"
	"encoding/hex"
)

// Phone returns a short opaque hash for a phone number or other stable
// identifier. Empty input returns the literal "<empty>" so a log line never
// has a bare blank value next to its key.
func Phone(p string) string {
	if p == "" {
		return "<empty>"
	}
	sum := sha256.Sum256([]byte(p))
	return "ph:" + hex.EncodeToString(sum[:4]) // 8 hex chars = 32 bits, plenty for correlation
}

// JID returns a short opaque hash for a WhatsApp group/user JID. Distinguished
// from Phone only by prefix — different namespace, same one-way semantics.
func JID(j string) string {
	if j == "" {
		return "<empty>"
	}
	sum := sha256.Sum256([]byte(j))
	return "g:" + hex.EncodeToString(sum[:4])
}

// Name returns a redaction of a display name suitable for logs: only the first
// whitespace-separated token ("Liza Katz" -> "Liza"). First names are far less
// identifying than full names while still letting humans recognize their own
// activity in traces.
func Name(n string) string {
	if n == "" {
		return "<empty>"
	}
	for i, r := range n {
		if r == ' ' || r == '\t' {
			return n[:i]
		}
	}
	return n
}
