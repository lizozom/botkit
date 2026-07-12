package bot

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Kind classifies an inbound message's primary content.
type Kind string

const (
	KindText     Kind = "text"
	KindImage    Kind = "image"
	KindVideo    Kind = "video"
	KindAudio    Kind = "audio"
	KindDocument Kind = "document"
	KindSticker  Kind = "sticker"
	KindContact  Kind = "contact"
	KindLocation Kind = "location"
	KindReaction Kind = "reaction"
	KindOther    Kind = "other" // not normalized — read Raw
)

// InboundMessage is the normalized envelope handed to OnGroupMessage /
// OnDirectMessage. The common 90% is normalized; anything else is reachable via
// Raw. See ../SPEC.md §10.
type InboundMessage struct {
	ID          string
	GroupID     string // "" for a DM
	SenderPhone string // E.164 digits (no '+'); "" if unresolvable (LID-only)
	SenderName  string
	Timestamp   time.Time
	IsDM        bool
	IsFromMe    bool

	Kind     Kind
	Text     string // plain text AND captions, unified
	Media    *Media // non-nil for downloadable media when AcceptMedia is on
	Contacts []Contact
	Location *Location
	Reaction *Reaction
	Mentions []string // phone/JID user parts
	Quoted   *Quoted  // the "previous message" reply context

	Raw *waE2E.Message // escape hatch — always present

	reply func(ctx context.Context, text string) error
}

var errNoReply = errors.New("reply unavailable on this message")

// Reply sends a reactive text reply into the chat this message came from. This
// is the only send path in botkit v1 (see ../SPEC.md §7).
func (m InboundMessage) Reply(ctx context.Context, text string) error {
	if m.reply == nil {
		return errNoReply
	}
	return m.reply(ctx, text)
}

// Media describes a downloadable attachment.
type Media struct {
	Kind        Kind
	MIME        string
	Filename    string
	Size        int
	IsVoiceNote bool
	IsViewOnce  bool

	download func(ctx context.Context) ([]byte, error)
}

var errNoMedia = errors.New("no downloadable media")

// Download fetches and decrypts the media bytes.
func (m *Media) Download(ctx context.Context) ([]byte, error) {
	if m == nil || m.download == nil {
		return nil, errNoMedia
	}
	return m.download(ctx)
}

// Contact is a shared contact card.
type Contact struct {
	DisplayName string
	VCard       string
}

// Phones parses TEL entries out of the vCard.
func (c Contact) Phones() []string {
	var out []string
	for _, line := range strings.Split(c.VCard, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "TEL") {
			continue
		}
		if i := strings.LastIndex(line, ":"); i >= 0 {
			if v := strings.TrimSpace(line[i+1:]); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// Location is a shared location.
type Location struct {
	Lat, Lng float64
	Name     string
	Address  string
}

// Quoted is the message this one replied to.
type Quoted struct {
	SenderPhone string
	Text        string
}

// Reaction is an emoji reaction to another message.
type Reaction struct {
	Emoji    string
	TargetID string
}

// extractText returns the text of a plain/extended-text message (not captions;
// those are folded in per-media at build time).
func extractText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if t := msg.GetConversation(); t != "" {
		return t
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	return ""
}

// unwrapViewOnce returns the inner message and true when msg is a view-once
// wrapper, so downstream detection treats it like a normal media message.
func unwrapViewOnce(msg *waE2E.Message) (*waE2E.Message, bool) {
	if v := msg.GetViewOnceMessage(); v != nil && v.GetMessage() != nil {
		return v.GetMessage(), true
	}
	if v := msg.GetViewOnceMessageV2(); v != nil && v.GetMessage() != nil {
		return v.GetMessage(), true
	}
	return msg, false
}

// contextInfoOf returns the ContextInfo from whichever sub-message carries it
// (used for quoted messages and mentions).
func contextInfoOf(msg *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetContextInfo()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetContextInfo()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetContextInfo()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetContextInfo()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage().GetContextInfo()
	}
	return nil
}
