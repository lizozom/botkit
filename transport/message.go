package transport

import (
	"context"

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
// reply to an inbound message). Errors pass through send.Classify so callers
// can distinguish a per-peer failure from a bot-wide outage.
func (c *Client) SendText(ctx context.Context, to types.JID, text string) error {
	_, err := c.wm.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	return send.Classify(err)
}

// DownloadAny downloads whatever media a message carries, decrypting the blob.
func (c *Client) DownloadAny(ctx context.Context, msg *waE2E.Message) ([]byte, error) {
	return c.wm.DownloadAny(ctx, msg)
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
