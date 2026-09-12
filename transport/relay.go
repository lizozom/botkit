package transport

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/lizozom/botkit/send"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SendImage uploads and sends an image to a chat. It is the media half of the
// relay tier (SPEC §7, tier 1.5) and is only ever reached through
// bot.InboundMessage.Relay, which binds it to an inbound message and a single
// configured destination.
//
// Like SendText it pauses a jittered interval first, so a burst of relayed
// media doesn't leave in one machine-instant volley. There is no "typing…"
// indicator: WhatsApp shows a media-upload presence instead, and faking a
// compose for an image the bot didn't write is the wrong signal.
//
// SendImage blocks for seconds by design — call it off any hot event goroutine.
func (c *Client) SendImage(ctx context.Context, to types.JID, data []byte, mime, caption string) error {
	relayPause(ctx)

	up, err := c.wm.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		// An upload failure is classified too: a dead socket fails here first.
		return send.Classify(err)
	}

	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype:      proto.String(mime),
		URL:           proto.String(up.URL),
		DirectPath:    proto.String(up.DirectPath),
		MediaKey:      up.MediaKey,
		FileEncSHA256: up.FileEncSHA256,
		FileSHA256:    up.FileSHA256,
		FileLength:    proto.Uint64(up.FileLength),
	}}
	if caption != "" {
		msg.ImageMessage.Caption = proto.String(caption)
	}

	_, err = c.wm.SendMessage(ctx, to, msg)
	return send.Classify(err)
}

// relayPause is a jittered [1s, 4s) gap before a relayed send. Shorter than
// humanizeDelay — a relay is a forward, not a composed reply — but never zero,
// so a kindergarten dumping forty photos at once doesn't become forty
// simultaneous sends.
func relayPause(ctx context.Context) {
	sleepCtx(ctx, time.Second+time.Duration(rand.IntN(3000))*time.Millisecond)
}
