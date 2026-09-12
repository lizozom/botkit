package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lizozom/botkit/send"
	"go.mau.fi/whatsmeow/types"
)

// ErrRelayDisabled is returned by Relay when Config.RelayTarget is unset.
var ErrRelayDisabled = errors.New("relay disabled: set Config.RelayTarget")

// ErrRelayCapReached is returned once the day's relay budget is spent. It is
// deliberately not an ErrBotWide — the session is fine, the bot is just done
// sending for today.
var ErrRelayCapReached = errors.New("relay daily cap reached")

// DefaultRelayDailyCap bounds relayed sends per UTC day when Config leaves it
// zero. Sized for the motivating case (a kindergarten posting a few dozen
// photos a day) with headroom, not for broadcasting.
const DefaultRelayDailyCap = 200

// OutboundMedia is the payload of a relayed send. Only images ship in v1 —
// the tier exists to forward photos, and every extra kind is extra send
// surface to justify.
type OutboundMedia struct {
	// Data is the raw, decrypted bytes. Typically straight from
	// InboundMessage.Media.Download.
	Data []byte
	// MIME is the content type, e.g. "image/jpeg". Required.
	MIME string
}

// relayLimiter enforces the per-day cap and the bot-wide halt. It lives for the
// process; the cap resets on the UTC day boundary, and a restart resets it too
// — a deliberate simplification, since a crash-looping bot has a louder problem
// than its relay budget.
type relayLimiter struct {
	mu     sync.Mutex
	cap    int
	day    string
	count  int
	halted bool
}

func (l *relayLimiter) take(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.halted {
		return fmt.Errorf("%w: relay halted after a bot-wide send failure", send.ErrBotWide)
	}
	if day := now.UTC().Format("2006-01-02"); day != l.day {
		l.day, l.count = day, 0
	}
	if l.count >= l.cap {
		return ErrRelayCapReached
	}
	l.count++
	return nil
}

// halt latches the limiter closed after a bot-wide failure, so a dead session
// doesn't produce one failed send per inbound photo for the rest of the day.
// Cleared on reconnect.
func (l *relayLimiter) halt() {
	l.mu.Lock()
	l.halted = true
	l.mu.Unlock()
}

func (l *relayLimiter) resume() {
	l.mu.Lock()
	l.halted = false
	l.mu.Unlock()
}

// relay is the bound implementation handed to each InboundMessage. The target
// comes from Config, never from the call site: that is what keeps this tier
// reactive-by-construction rather than a broadcast primitive in disguise.
func (b *Bot) relay(ctx context.Context, media OutboundMedia, caption string) error {
	if b.relayTarget.IsEmpty() {
		return ErrRelayDisabled
	}
	if len(media.Data) == 0 {
		return errors.New("relay: empty media")
	}
	if media.MIME == "" {
		return errors.New("relay: missing MIME")
	}
	if err := b.relayLimit.take(time.Now()); err != nil {
		return err
	}
	err := b.tp.SendImage(ctx, b.relayTarget, media.Data, media.MIME, caption)
	if errors.Is(err, send.ErrBotWide) {
		b.relayLimit.halt()
	}
	return err
}

// RelayTarget returns the single configured relay destination, or the zero JID
// when relaying is off.
func (b *Bot) RelayTarget() types.JID { return b.relayTarget }
