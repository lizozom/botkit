// Package send holds the outbound-error taxonomy for botkit. v1 ships only the
// reactive Reply (a method on bot.InboundMessage) — there is deliberately no
// raw Send primitive. See ../SPEC.md section 7.
//
// Classify sorts a whatsmeow send error into two buckets so a scheduler (or the
// caller) can tell a per-peer problem apart from a bot-wide outage:
//   - ErrPeerUnreachable: this one chat is gone; other sends still work.
//   - ErrBotWide: the session/socket is down; every send fails until re-pair
//     or reconnect. Lifted from whatsapp-nagger's classifySendErr.
package send

import (
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
)

// ErrPeerUnreachable marks a permanent failure for one specific chat (e.g. the
// bot was removed from the group). Other chats are unaffected.
var ErrPeerUnreachable = errors.New("peer unreachable")

// ErrBotWide marks a failure that affects EVERY outbound message until a human
// re-pairs or the websocket reconnects (no session, no socket, not paired).
var ErrBotWide = errors.New("bot-wide failure")

// Classify wraps err with ErrPeerUnreachable or ErrBotWide when it recognizes
// the shape, else returns it untouched. The original is wrapped via %w so
// callers can still errors.As the underlying whatsmeow error.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, whatsmeow.ErrNotInGroup) || errors.Is(err, whatsmeow.ErrGroupNotFound) {
		return fmt.Errorf("%w: %w", ErrPeerUnreachable, err)
	}
	if isBotWide(err) {
		return fmt.Errorf("%w: %w", ErrBotWide, err)
	}
	return err
}

func isBotWide(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, whatsmeow.ErrNotLoggedIn) {
		return true
	}
	msg := err.Error()
	for _, marker := range botWideMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// botWideMarkers are error-message substrings whatsmeow (and our own send
// wrapper) surface for a broken session. Add new wordings as they show up.
var botWideMarkers = []string{
	"the store doesn't contain a device JID", // whatsmeow when Store.ID == nil
	"device JID missing",                      // our own Ping wrapper
	"not paired yet",                          // our own guard
	"websocket not connected",                 // whatsmeow socket layer
}
