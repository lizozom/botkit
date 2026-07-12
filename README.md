# botkit

A reusable Go library for building **WhatsApp** group bots. It hides the
whatsmeow lifecycle, pairing, media handling, scheduling, group gating,
observability, and group-membership web auth behind a small, hard-to-misuse
surface — so an app is just its handlers plus a config.

Extracted from the shared plumbing of two production bots
(`whatsapp-nagger`, `amit/whatsapp-mgr`), with the anti-ban lessons from their
Meta post-mortems baked in (manual-only pairing, reactive-only sends,
fail-closed group gating).

- **Design:** [`SPEC.md`](SPEC.md)
- **Usage examples:** [`docs/examples.md`](docs/examples.md)

## Quick start

An app is a `Config` plus handlers. This is a complete reply bot:

```go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/lizozom/botkit/bot"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := bot.New(bot.Config{
		SessionDBPath: "whatsapp_session.db",
		BotPhone:      os.Getenv("BOT_PHONE"),          // for manual pairing only
		ManagedGroups: []string{"12036...@g.us"},       // fail-closed whitelist
		OpsAddr:       ":8080",                          // private pairing API
		OpsToken:      os.Getenv("PAIR_TOKEN"),
	})
	if err != nil {
		panic(err)
	}

	// Reply in a managed group. Reactive + humanized (typing + jitter);
	// there is no way to send un-prompted — see SPEC §7.
	b.OnGroupMessage(func(ctx context.Context, msg bot.InboundMessage) error {
		if msg.Text == "ping" {
			return msg.Reply(ctx, "pong 🏓")
		}
		return nil
	})

	// DMs, only from people already in a managed group (fail-closed).
	// Omit this call entirely to ignore DMs.
	b.OnDirectMessage(bot.DMsFromMembers, func(ctx context.Context, msg bot.InboundMessage) error {
		return msg.Reply(ctx, "talk to me in the group 🙂")
	})

	if err := b.Run(ctx); err != nil { // connects, dispatches, blocks
		panic(err)
	}
}
```

**Pairing is manual** (the bot never pairs itself). First run, over the private ops port:

```bash
curl -X POST -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8080/pair
# enter the returned code: WhatsApp → Linked Devices → Link with phone number
curl -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8080/groups   # discover JIDs
```

Handling media (invoices, receipts) — set `AcceptMedia: true`, then:

```go
b.OnGroupMessage(func(ctx context.Context, msg bot.InboundMessage) error {
	if msg.Media != nil { // image | video | audio | document | sticker
		blob, err := msg.Media.Download(ctx) // one path for any kind
		if err != nil {
			return err
		}
		_ = blob // hand to your OCR / LLM / storage
		return msg.Reply(ctx, "got it 📎")
	}
	return nil
})
```

Scheduled jobs (`OnSchedule` — join-request gatekeeper, audits, syncs) and the
membership-gated dashboard login (`webauth`) are Phase 3, landing next. See
[`docs/examples.md`](docs/examples.md) for the full envelope, DM policies, and
the AMIT gatekeeper shape.

## Status

Design locked; implementation in progress.

| Package | Purpose | State |
|---|---|---|
| `redact` | one-way PII hashing for logs | ✅ done |
| `telemetry` | OTLP-logs bootstrap (`Init`) | ✅ done |
| `identity` | phone canonicalization (`Normalize`) | ✅ done; LID resolution lives in `transport` |
| `transport` | whatsmeow connect/session/pair/lifecycle | ✅ done (Phase 1) |
| `pairing` | manual pair flow + ops API (`/pair`,`/status`,`/groups`) | ✅ done (Phase 1) |
| `store` | session-DB open (`OpenSQLite`) | ✅ session open; metadata KV + run bookkeeping → Phase 3 |
| `bot` | orchestrator, `OnGroupMessage`/`OnDirectMessage`, envelope | ✅ done (Phase 2) |
| `send` | `Reply` error taxonomy (`ErrBotWide`/`ErrPeerUnreachable`) | ✅ done (Phase 2) |
| `gate` | fail-closed group whitelist | ✅ done (Phase 2) |
| `schedule` | job kernel (jitter, idempotency) | ⬜ Phase 3 |
| `webauth` | membership-gated dashboard auth | ⬜ Phase 3 |

**Demo:** `cmd/hello` — a real reply bot in ~90 lines. Connects to WhatsApp
(manual pairing), replies `pong` to `ping` in a managed group, and nudges
member DMs back to the group. See `cmd/hello/.env.example`. Message handling
(`OnGroupMessage`/`OnDirectMessage`, media envelope) is Phase 2; schedulers and
dashboard auth are Phase 3.

## Develop

```bash
go build ./...
go test ./...
```
