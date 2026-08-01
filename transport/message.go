package transport

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/lizozom/botkit/identity"
	"github.com/lizozom/botkit/send"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// SetOnMessage registers a hook for inbound message events. The bot layer
// translates these into its envelope, gates them, and dispatches to handlers.
func (c *Client) SetOnMessage(fn func(*events.Message)) { c.onMessage = fn }

// SendText sends a plain-text message to a chat (group or user). This is the
// only send primitive botkit exposes, and it is always used reactively (in
// reply to an inbound message). It humanizes first — a "typing…" indicator and
// a short jittered pause — so replies feel composed rather than machine-instant
// (a small but real anti-abuse signal). Errors pass through send.Classify so
// callers can distinguish a per-peer failure from a bot-wide outage.
//
// SendText blocks for a couple of seconds by design; call it off any hot event
// goroutine (the bot layer dispatches handlers asynchronously).
func (c *Client) SendText(ctx context.Context, to types.JID, text string) error {
	c.humanize(ctx, to)
	_, err := c.wm.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	return send.Classify(err)
}

// humanize shows the WhatsApp "typing…" indicator and pauses a small random
// interval before returning. Best-effort: a presence-send failure is ignored
// (the message still goes out). WhatsApp's typing indicator times out ~10s, so
// for longer pauses we refresh it halfway through.
func (c *Client) humanize(ctx context.Context, chat types.JID) {
	_ = c.wm.SendChatPresence(ctx, chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	delay := humanizeDelay()
	if delay > 6*time.Second {
		sleepCtx(ctx, 5*time.Second)
		_ = c.wm.SendChatPresence(ctx, chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		sleepCtx(ctx, delay-5*time.Second)
	} else {
		sleepCtx(ctx, delay)
	}
}

// humanizeDelay returns a uniform random pause in [2s, 7s).
func humanizeDelay() time.Duration {
	return 2*time.Second + time.Duration(rand.IntN(5000))*time.Millisecond
}

// approvePause is a short humanized delay [1s, 3s) before a group approval, so
// a batch of admits doesn't fire in one machine-instant burst.
func approvePause(ctx context.Context) {
	sleepCtx(ctx, time.Second+time.Duration(rand.IntN(2000))*time.Millisecond)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// DownloadAny downloads whatever media a message carries, decrypting the blob.
func (c *Client) DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	return c.wm.DownloadAny(ctx, msg)
}

// IsMember reports whether member is a participant of group right now. This is
// webauth's authorization check — a dashboard login lives or dies on it — so it
// always asks WhatsApp rather than trusting anything cached.
//
// Matching spans both identity namespaces. WhatsApp addresses the same person
// as a phone JID or as a LID depending on the group's privacy mode, and an
// inbound message carries whichever form that group uses, so the member JID is
// compared against each of a participant's forms via sameJID (User AND Server —
// see its comment for why the Server half is load-bearing). The phone fallback
// below covers the case where the two sides know the person under different
// forms and the LID map can bridge them; it is gated on a successful resolution
// on both sides so that two unresolvable identities never compare equal.
func (c *Client) IsMember(ctx context.Context, group, member types.JID) (bool, error) {
	if group.IsEmpty() || member.IsEmpty() || member.User == "" {
		return false, nil
	}
	var info *types.GroupInfo
	err := c.withRateRetry(ctx, func() error {
		var e error
		info, e = c.wm.GetGroupInfo(ctx, group)
		return e
	})
	if err != nil {
		return false, fmt.Errorf("get group info: %w", err)
	}

	wantPhone := identity.Normalize(c.ResolvePhone(ctx, member))
	for _, p := range info.Participants {
		if sameJID(p.JID, member) || sameJID(p.PhoneNumber, member) || sameJID(p.LID, member) {
			return true, nil
		}
		if wantPhone == "" {
			continue // member's phone is unknown; JID forms above were the only chance
		}
		phone := p.PhoneNumber.User
		if phone == "" {
			phone = c.ResolvePhone(ctx, p.JID)
		}
		if phone != "" && identity.Normalize(phone) == wantPhone {
			return true, nil
		}
	}
	return false, nil
}

// IsMemberOfAny reports whether phone is a current participant of any of the
// given groups. Used to gate DMs to people already in a managed group.
func (c *Client) IsMemberOfAny(ctx context.Context, phone string, groups []types.JID) (bool, error) {
	want := identity.Normalize(phone)
	if want == "" {
		return false, nil
	}
	for _, g := range groups {
		members, err := c.Members(ctx, g)
		if err != nil {
			return false, err
		}
		for _, m := range members {
			if identity.Normalize(m.Phone) == want {
				return true, nil
			}
		}
	}
	return false, nil
}
