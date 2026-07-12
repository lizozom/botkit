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
| `bot` | orchestrator, handlers, actions, envelope | ⬜ Phase 2 |
| `send` | reactive `Reply` + error taxonomy | ⬜ Phase 2 |
| `gate` | fail-closed group whitelist | ⬜ Phase 2 |
| `schedule` | job kernel (jitter, idempotency) | ⬜ Phase 3 |
| `webauth` | membership-gated dashboard auth | ⬜ Phase 3 |

**Demo:** `cmd/hello` — opens a session, serves the pairing ops API, and
connects to WhatsApp for real (manual pairing). See `cmd/hello/.env.example`.
It does not reply to messages yet — that arrives with Phase 2 (`OnMessage` +
`Reply`).

## Develop

```bash
go build ./...
go test ./...
```
