// Package bot is botkit's orchestrator: it ties the WhatsApp transport, group
// gate, pairing ops API, and message handlers together behind a small surface.
// An app builds a Bot from a Config, registers handlers, and calls Run.
// See ../SPEC.md sections 5, 6, 10.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/lizozom/botkit/gate"
	"github.com/lizozom/botkit/pairing"
	"github.com/lizozom/botkit/store"
	"github.com/lizozom/botkit/transport"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// DMPolicy decides which direct messages reach an OnDirectMessage handler.
type DMPolicy int

const (
	// DMsFromMembers accepts DMs only from people in one of the managed groups.
	DMsFromMembers DMPolicy = iota + 1
	// DMsFromAnyone accepts DMs from any sender.
	DMsFromAnyone
)

// Handler processes an inbound message. A returned error is logged.
type Handler func(ctx context.Context, msg InboundMessage) error

// Bot is a running WhatsApp bot. Build with New; drive with Run.
type Bot struct {
	cfg    Config
	groups *gate.Groups
	tp     *transport.Client
	kv     *store.KV
	ctx    context.Context

	onGroup  Handler
	onDM     Handler
	dmPolicy DMPolicy
	jobs     []scheduledJob
}

// New validates config and builds the bot. It does not touch the network —
// call Run to open the session and go online.
func New(cfg Config) (*Bot, error) {
	var groups *gate.Groups
	if cfg.AllGroups {
		groups = gate.AllowAll()
	} else {
		var err error
		if groups, err = gate.ParseGroups(cfg.ManagedGroups); err != nil {
			return nil, err
		}
	}
	return &Bot{cfg: cfg, groups: groups}, nil
}

// OnGroupMessage registers the handler for messages in managed groups.
func (b *Bot) OnGroupMessage(fn Handler) { b.onGroup = fn }

// OnDirectMessage registers the handler for 1:1 DMs, gated by policy. Not
// calling this leaves DMs off entirely (fail-closed).
func (b *Bot) OnDirectMessage(policy DMPolicy, fn Handler) {
	b.dmPolicy = policy
	b.onDM = fn
}

// Run opens the session, wires the ops API, connects (or idles if unpaired),
// dispatches messages, and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.ctx = ctx

	tp, err := transport.New(ctx, b.cfg.SessionDBPath)
	if err != nil {
		return err
	}
	b.tp = tp
	tp.SetOnMessage(b.dispatch)

	// Open the KV (scheduler idempotency, future webauth nonces) only if needed.
	if len(b.jobs) > 0 {
		kvPath := filepath.Join(filepath.Dir(b.cfg.SessionDBPath), "botkit_state.db")
		kv, err := store.NewKV(kvPath)
		if err != nil {
			return fmt.Errorf("open state kv: %w", err)
		}
		b.kv = kv
		defer func() { _ = b.kv.Close() }()
	}

	// Scheduled jobs start once, on first connect — never tick into a dead socket.
	var startOnce sync.Once
	tp.SetOnConnected(func() {
		slog.Info("botkit: connected", slog.String("self", tp.SelfJID().String()))
		if len(b.jobs) > 0 {
			startOnce.Do(func() { b.startJobs(ctx) })
		}
	})
	tp.SetOnLoggedOut(func(reason string) {
		slog.Error("botkit: session lost — manual re-pair required (no auto-pair)",
			slog.String("reason", reason))
	})

	switch {
	case b.groups.AllowsAll():
		slog.Warn("botkit: AllGroups is ON — the bot will respond in EVERY group the number belongs to (fail-open)")
	case b.onGroup != nil && b.groups.Empty():
		slog.Warn("botkit: OnGroupMessage registered but ManagedGroups is empty — the bot will act in NO groups")
	}

	var srv *http.Server
	if b.cfg.OpsAddr != "" {
		ops := pairing.New(b.cfg.OpsToken, &pairAdapter{tp: tp, phone: b.cfg.BotPhone, ctx: ctx})
		srv = &http.Server{Addr: b.cfg.OpsAddr, Handler: ops.Handler()}
		go func() {
			slog.Info("botkit: ops API listening (keep private)", slog.String("addr", b.cfg.OpsAddr))
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("botkit: ops server", slog.String("err", err.Error()))
			}
		}()
	}

	switch err := tp.Connect(ctx); {
	case errors.Is(err, transport.ErrNotPaired):
		slog.Warn("botkit: not paired — trigger pairing via POST /pair")
	case err != nil:
		return fmt.Errorf("connect: %w", err)
	default:
		slog.Info("botkit: connecting with saved session…")
	}

	<-ctx.Done()
	if srv != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}
	tp.Disconnect()
	return nil
}

func (b *Bot) dispatch(evt *events.Message) {
	if evt.Info.IsFromMe {
		return // never react to our own messages
	}
	// Run off whatsmeow's event goroutine: handlers (and the humanized send)
	// can block for seconds, and must not stall the socket's event loop.
	go b.handle(evt)
}

func (b *Bot) handle(evt *events.Message) {
	chat := evt.Info.Chat
	isGroup := chat.Server == types.GroupServer

	if isGroup {
		if b.onGroup == nil || !b.groups.Allows(chat) {
			return // no handler, or unmanaged group (fail-closed)
		}
		b.run(b.onGroup, b.buildMessage(evt, false), "OnGroupMessage")
		return
	}

	// DM
	if b.onDM == nil {
		return // DMs off (fail-closed)
	}
	if b.dmPolicy == DMsFromMembers {
		phone := b.tp.ResolvePhone(b.ctx, evt.Info.Sender)
		ok, err := b.tp.IsMemberOfAny(b.ctx, phone, b.groups.List())
		if err != nil {
			slog.Warn("botkit: DM membership check failed — dropping DM", slog.String("err", err.Error()))
			return
		}
		if !ok {
			return // sender not in any managed group
		}
	}
	b.run(b.onDM, b.buildMessage(evt, true), "OnDirectMessage")
}

func (b *Bot) run(fn Handler, msg InboundMessage, label string) {
	if err := fn(b.ctx, msg); err != nil {
		slog.Error("botkit: handler error", slog.String("handler", label), slog.String("err", err.Error()))
	}
}

func (b *Bot) buildMessage(evt *events.Message, isDM bool) InboundMessage {
	info := evt.Info
	m := InboundMessage{
		ID:          info.ID,
		SenderName:  senderName(evt),
		SenderPhone: b.tp.ResolvePhone(b.ctx, info.Sender),
		Timestamp:   info.Timestamp,
		IsDM:        isDM,
		IsFromMe:    info.IsFromMe,
		Kind:        KindText,
		Raw:         evt.Message,
	}
	if !isDM {
		m.GroupID = info.Chat.String()
	}
	chat := info.Chat
	m.reply = func(ctx context.Context, text string) error {
		return b.tp.SendText(ctx, chat, text)
	}

	inner, viewOnce := unwrapViewOnce(evt.Message)
	m.Text = extractText(inner)

	if ci := contextInfoOf(inner); ci != nil {
		m.Mentions = ci.GetMentionedJID()
		if q := ci.GetQuotedMessage(); q != nil {
			m.Quoted = &Quoted{Text: extractText(q)}
		}
	}

	switch {
	case inner.GetImageMessage() != nil:
		im := inner.GetImageMessage()
		m.Kind = KindImage
		m.Text = firstNonEmpty(m.Text, im.GetCaption())
		m.Media = b.mediaFor(evt.Message, KindImage, im.GetMimetype(), "", int(im.GetFileLength()), false, viewOnce)
	case inner.GetVideoMessage() != nil:
		vm := inner.GetVideoMessage()
		m.Kind = KindVideo
		m.Text = firstNonEmpty(m.Text, vm.GetCaption())
		m.Media = b.mediaFor(evt.Message, KindVideo, vm.GetMimetype(), "", int(vm.GetFileLength()), false, viewOnce)
	case inner.GetAudioMessage() != nil:
		am := inner.GetAudioMessage()
		m.Kind = KindAudio
		m.Media = b.mediaFor(evt.Message, KindAudio, am.GetMimetype(), "", int(am.GetFileLength()), am.GetPTT(), viewOnce)
	case inner.GetDocumentMessage() != nil:
		dm := inner.GetDocumentMessage()
		m.Kind = KindDocument
		m.Text = firstNonEmpty(m.Text, dm.GetCaption())
		m.Media = b.mediaFor(evt.Message, KindDocument, dm.GetMimetype(), dm.GetFileName(), int(dm.GetFileLength()), false, viewOnce)
	case inner.GetStickerMessage() != nil:
		sm := inner.GetStickerMessage()
		m.Kind = KindSticker
		m.Media = b.mediaFor(evt.Message, KindSticker, sm.GetMimetype(), "", int(sm.GetFileLength()), false, viewOnce)
	case inner.GetContactMessage() != nil:
		cm := inner.GetContactMessage()
		m.Kind = KindContact
		m.Contacts = []Contact{{DisplayName: cm.GetDisplayName(), VCard: cm.GetVcard()}}
	case inner.GetContactsArrayMessage() != nil:
		m.Kind = KindContact
		for _, cm := range inner.GetContactsArrayMessage().GetContacts() {
			m.Contacts = append(m.Contacts, Contact{DisplayName: cm.GetDisplayName(), VCard: cm.GetVcard()})
		}
	case inner.GetLocationMessage() != nil:
		lm := inner.GetLocationMessage()
		m.Kind = KindLocation
		m.Location = &Location{Lat: lm.GetDegreesLatitude(), Lng: lm.GetDegreesLongitude(), Name: lm.GetName(), Address: lm.GetAddress()}
	case inner.GetReactionMessage() != nil:
		rm := inner.GetReactionMessage()
		m.Kind = KindReaction
		m.Reaction = &Reaction{Emoji: rm.GetText(), TargetID: rm.GetKey().GetID()}
	case m.Text == "":
		m.Kind = KindOther // unnormalized (poll, live location, business…) — read Raw
	}
	return m
}

func (b *Bot) mediaFor(raw *waE2E.Message, kind Kind, mime, filename string, size int, voice, viewOnce bool) *Media {
	if !b.cfg.AcceptMedia {
		return nil // media opt-in
	}
	return &Media{
		Kind: kind, MIME: mime, Filename: filename, Size: size,
		IsVoiceNote: voice, IsViewOnce: viewOnce,
		download: func(ctx context.Context) ([]byte, error) {
			return b.tp.DownloadAny(ctx, raw)
		},
	}
}

func senderName(evt *events.Message) string {
	if evt.Info.PushName != "" {
		return evt.Info.PushName
	}
	return evt.Info.Sender.User
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// pairAdapter bridges *transport.Client to pairing.WhatsApp: it binds the
// configured phone and the process-lifetime context (never the HTTP request
// context, which would tear the socket down when the response returns), and
// maps GroupSummary to the ops API's Group shape.
type pairAdapter struct {
	tp    *transport.Client
	phone string
	ctx   context.Context
}

func (a *pairAdapter) Paired() bool    { return a.tp.Paired() }
func (a *pairAdapter) Connected() bool { return a.tp.Connected() }
func (a *pairAdapter) Pair(context.Context) (string, error) {
	return a.tp.Pair(a.ctx, a.phone)
}
func (a *pairAdapter) Groups(context.Context) ([]pairing.Group, error) {
	gs, err := a.tp.AllGroups(a.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pairing.Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, pairing.Group{JID: g.JID.String(), Name: g.Name, IsAdmin: g.IsAdmin, Members: len(g.Members)})
	}
	return out, nil
}
