# botkit — spec

A reusable Go library for building **WhatsApp** group bots, extracted from the shared
plumbing of two existing bots (`whatsapp-nagger` and `amit/whatsapp-mgr`). It hides the
whatsmeow lifecycle, pairing, media handling, scheduling, group gating, observability, and
group-membership web auth behind a small, hard-to-misuse surface — so an app is just its
handlers plus a config.

**Status:** design locked, not yet implemented.
**Scope of v1:** WhatsApp only.

---

## 1. Why this exists

`whatsapp-nagger` (an LLM agent bot; now banned by Meta) and `amit/whatsapp-mgr` (a
deterministic group-join gatekeeper; live on Fly) already carry **divergent copies of the
same plumbing**: whatsmeow connection, pairing, scheduler, OTEL telemetry, redaction,
config, SQLite store, an ops HTTP API. AMIT was written second as a de-domained rewrite of
nagger's patterns, *after* the ban post-mortems — so its versions are the cleaner,
hardened ones.

`botkit` DRYs those two forks into one library and folds in the hard-won anti-ban lessons
so no future consumer can repeat nagger's mistakes by accident.

### Consumers (the reason the interface must be right)

1. **AMIT** (`whatsapp-mgr`) — join-request gatekeeper. Migrate onto `botkit` (lowest risk;
   it's already the reference shape).
2. **travel-expenses** — new invoice-collector bot for group tour managers + a read-only
   dashboard. Built on `botkit` from day one.
3. **nudnik** (nagger reborn) — moving to **pure Telegram**. Does **not** ride `botkit` in
   v1; waits for the phase-2 Telegram transport.

## 2. Non-goals (v1)

- **No Telegram transport.** The handler seam is designed transport-neutral so Telegram is a
  purely additive phase 2, but zero Telegram code ships now.
- **No proactive / broadcast sending.** No digests, nags, farewells, or bulk DMs. Nagger died
  from proactive send *volume*; the send surface is `Reply`-only (see §7).
- **No outbound media.** Replies are text. Media send is added when a consumer needs it.
- **No transcription / media interpretation.** Inbound audio/images are delivered as bytes;
  what happens to them is the app's business.
- **No LLM/agent layer.** Tool-calling, prompts, conversation loops are app-side. `botkit`
  has no opinion about what an `OnMessage` handler does inside.
- **No domain storage.** SQLite remains each app's source of truth for its own tables.

## 3. Architecture

**Observe → dispatch → act**, with all whatsmeow contact hidden inside the library.

- The app builds a `bot.Bot` from a `bot.Config`, registers handlers, and calls `Run(ctx)`.
- `main` owns lifecycle (signal context, connect, blocking). The library never hides
  connect/pair/signal handling inside a constructor (nagger's mistake).
- Two **handler** types (§6) — one event-driven, one timer-driven — plus a set of **guarded
  WhatsApp action methods** (§6.3) that handlers call. Every WhatsApp *write* carries its
  anti-ban hygiene *inside* the library method.
- Inbound events from non-managed groups are dropped **before** any handler runs (§8).

### Package layout

```
botkit/
  bot/         Bot orchestrator: New, Run, handler registration, action methods, lifecycle
  transport/   WhatsApp transport (whatsmeow): connect, session, events → normalized envelope
  pairing/     manual-only pair flow + ops API endpoints (/pair, /status, /groups)
  send/        the outbound path — Reply-only in v1; the guardrail lives here
  schedule/    tick + jitter + overlap-guard + idempotency + connected-gating kernel
  identity/    resolver.Normalize + LID↔PN resolution + participant matching
  gate/        group whitelist (JID, fail-closed) + optional phone allowlist
  webauth/     membership-gated magic-link → session (batteries-included, §9)
  store/        session-DB open + generic metadata KV + scheduler-run bookkeeping
  redact/      Phone/JID/Name hashing for logs (extracted verbatim)
  telemetry/   OTLP-logs Init (extracted verbatim); event emission stays app-side
```

## 4. Extraction map (what comes from where)

**Clean lifts (near-identical in both repos, ~zero domain coupling):**
- `redact.Phone/JID` — already byte-for-byte duplicated.
- `telemetry.Init` — near-identical OTLP bootstrap.
- `identity` — AMIT's `resolver.Normalize` (pure) + `ResolvePhone` (3-tier LID→PN).
- whatsmeow session-DB open (same WAL/busy_timeout DSN in both).
- the pairing primitive (`GetQRChannel`→`Connect`→`PairPhone`, identical core).

**AMIT is the base for:** `transport` (its clean `wa.Client`), `pairing` (its `pairapi`),
`gate` (its required JID whitelist), `store` bookkeeping, config helpers, the `main`
bootstrap shape.

**nagger contributes the two things AMIT never had:** the inbound **message** seam
(`events.Message` handling, text/media extraction, quoted-reply) and the outbound **send**
path — including the `ErrPeerUnreachable`/`ErrBotWide` send-error taxonomy — but *stripped of*
its auto-pairing and proactive schedulers, which are the ban surface.

## 5. Bot construction

```go
b := bot.New(bot.Config{
    SessionDBPath: "/data/whatsapp_session.db",
    BotPhone:      cfg.Phone,          // for manual pairing only
    ManagedGroups: cfg.ManagedGroups,  // fail-closed JID whitelist (required, §8)
    OpsAddr:       ":8080",            // ops API + webauth redeem (localhost only)
    OpsToken:      cfg.PairToken,      // bearer for ops API
    AcceptMedia:   true,               // deliver media to OnMessage (default false)
    WebAuth:       &webauth.Config{...}, // optional; enables the dashboard auth flow
})

b.OnMessage(handleMessage)                                  // §6.1
b.OnSchedule("gatekeeper", schedule.EveryJittered(5*time.Minute, 2*time.Minute), sweep) // §6.2

if err := b.Run(ctx); err != nil { log.Fatal(err) } // blocks; main owns ctx/signals
```

`bot.Config` holds only **framework-owned** settings. Each app keeps its own domain config
(roster path, LLM keys, billing day, …) in its own struct, using `botkit`'s exported env
helpers (`Env`, `Dur`, `SplitList`, `Redacted`) if it wants them.

## 6. Handlers & actions

Two honest handler types — honest because each maps to something whatsmeow actually provides.

### 6.1 `OnMessage` — event-driven

whatsmeow *has* `events.Message`, so this is a real event callback.

```go
b.OnMessage(func(ctx context.Context, msg bot.InboundMessage) error {
    reply, err := agent.Handle(ctx, msg)  // app logic (LLM, rules, whatever)
    if err != nil { return err }
    return msg.Reply(ctx, reply)          // reactive send — always safe (§7)
})
```

### 6.2 `OnSchedule` — timer-driven, registered N times

whatsmeow has **no** join-request event, and periodic work (audits, rechecks, syncs) is
timer-driven anyway. So everything periodic is an `OnSchedule` job. Registered multiple
times; each is an independent named job. Example — AMIT's gatekeeper as a scheduled job that
calls action methods:

```go
b.OnSchedule("gatekeeper", schedule.EveryJittered(5*time.Minute, 2*time.Minute),
    func(ctx context.Context) error {
        for _, g := range b.ManagedGroups() {
            pending, err := b.PendingJoinRequests(ctx, g) // §6.3
            if err != nil { return err }
            for _, req := range pending {
                if roster.Approved(req.Phone) {
                    if err := b.ApproveJoin(ctx, g, req); err != nil { return err }
                }
            }
        }
        return nil
    })
b.OnSchedule("recheck", schedule.Every(1*time.Hour), recheckFn)
b.OnSchedule("audit",   schedule.DailyAt(9, "Asia/Jerusalem"), auditFn)
```

The `name` keys four guarantees the library provides so apps don't hand-roll them:

- **Overlap guard** — a job never starts a new run while its previous run is still going.
- **Daily idempotency across restarts** — `DailyAt` fires once per calendar day even if the
  process restarts at 09:05, recording the last-fired date in the metadata KV (generalizes
  nagger's `last_digest_date` / `ShouldFireDigest`).
- **Connected-gating** — jobs don't tick until the WhatsApp session is actually online
  (AMIT's `sync.Once` on `Connected`); no firing into a dead socket.
- **Staggered starts + jitter** — start times are offset and each interval jittered, so N
  jobs never fire in one burst at boot. Burst-avoidance is anti-ban hygiene.

Schedule kinds: `Every(d)`, `EveryJittered(d, jitter)`, `DailyAt(hour, tz)`.

### 6.3 Guarded WhatsApp action methods

Handlers call these; each WhatsApp write carries its hygiene inside:

| Method | Notes |
|---|---|
| `msg.Reply(ctx, text)` | reactive send; only send path in v1 (§7) |
| `PendingJoinRequests(ctx, group)` | polls `GetGroupRequestParticipants` with 429 backoff; LID→phone resolved |
| `ApproveJoin(ctx, group, req)` | `UpdateGroupRequestParticipants(...Approve)` with a 1–3s humanized pause |
| `GroupMembers(ctx, group)` | live participant list |
| `IsMember(ctx, group, identity)` | live membership check (used by `webauth`, §9) |
| `ManagedGroups()` | the configured JID whitelist |
| `AllGroups(ctx)` | every joined group (not just managed) — name, admin, member count, community linkage; for onboarding/diagnostics, not gating |

`OnPairingLost(fn func(reason string))`, registered before `Run`, fires once whenever the
session has no usable pairing — never paired at boot, or logged out later — so an app can
alert a human instead of hand-polling the ops API's `/status`.

## 7. The send surface (the ban guardrail)

Nagger died from proactive send **volume**. `botkit` makes that class of mistake structurally
hard. There is a tiered taxonomy; **only tiers 1 and 1.5 are implemented in v1.**

| Tier | Example | v1 status |
|---|---|---|
| **1. Reply** | answer a message in the group | **Implemented.** Bound to an inbound message, so it's reactive by construction. |
| **1.5 Relay** | forward a matching photo to one fixed chat | **Implemented.** Bound to an inbound message like Reply, but lands in a different chat. Destination is config, not a parameter. |
| **2. Transactional DM** | one-off OTP to someone who just asked | **Not built.** Obviated by the in-group magic-link auth (§9). |
| **3. Proactive / broadcast** | digest, nag, farewell | **Not built.** When needed, must go through forced throttle + jitter + engagement-gating + per-day cap + `ErrBotWide` halt. |

Consequences, on purpose:
- **There is no `bot.Send(anyGroup, text)` primitive.** The only sends are `msg.Reply` and
  `msg.Relay`, both bound to an inbound message, so neither can initiate an unsolicited
  message. No back door to fake a broadcast.
- The `ErrPeerUnreachable` (per-group) vs `ErrBotWide` (session-wide) send-error taxonomy is
  lifted from nagger and available for when tier 3 is eventually built.

### 7.1 The relay tier

Tier 1.5 exists for one shape: an app that watches a managed group and forwards a
*subset* of what it sees to the operator's own chat. The motivating case is a
kindergarten photo filter — dozens of group photos a day, of which a parent wants only
the ones containing their own child.

`Reply` cannot express this (wrong chat, and text-only), but the case is nothing like
tier 3 either: every outbound is caused 1:1 by an inbound, and there is exactly one
destination for the life of the process.

The guardrails are structural rather than advisory:

- **The destination is `Config.RelayTarget`, never a call-site argument.** A handler
  decides *whether* to relay, never *where*. `Relay(ctx, media, caption)` has no
  recipient parameter, so no amount of handler code turns it into a fan-out.
- **Bound to an inbound message**, exactly like `Reply` — `InboundMessage.Relay` cannot
  be called out of nowhere.
- **Per-day cap** (`RelayDailyCap`, default 200) returning `ErrRelayCapReached`. A
  runaway matcher stops at a budget instead of emptying a group into someone's DMs.
- **Jittered pause** of 1–4s before each send, so forty photos arriving at once do not
  become forty simultaneous sends.
- **Bot-wide halt latch**: the first `ErrBotWide` closes the relay until the socket
  reconnects, rather than producing one failed send per inbound for the rest of the day.
- **Images only.** Every additional media kind is additional send surface, and the tier
  was built to forward photos.

Empty `RelayTarget` disables the tier entirely; `Relay` then returns `ErrRelayDisabled`,
so a bot that never configures it has no relay surface at all.

## 8. Group gating

The bot logs into a **real WhatsApp number** already in many unrelated groups (AMIT's is in
~1131). whatsmeow delivers events from all of them. Ungated, the bot reacts in chats it has
no business in — noisy, and a fast track to a ban.

- `ManagedGroups` is a **JID whitelist**. Every inbound event (message, join-request poll)
  from a group **not** on the list is dropped **before** the handler runs.
- **Fail-closed:** an empty whitelist means the bot acts in **zero** groups (AMIT's default),
  not all of them (nagger's dangerous default). Boot refuses without it.
- JIDs are discovered via the ops API `/groups`.
- An optional secondary **phone allowlist** (nagger's `Allowlist`) can further filter *who*
  within a managed group the bot responds to.

## 9. Dashboard auth — membership-gated magic link (`webauth`)

Replaces the old `personas.md` allowlist + DM-OTP flow. Authorization is **live group
membership**, and login is an **in-group reply** (no DM → no non-reply send).

What triggers a mint is the app's business: a keyword, a regex, or an LLM agent inferring the
intent and calling `MintLink` as a tool. The trigger is never a security boundary — an agent
talked into minting for the wrong person still produces a link that fails the live membership
check at redeem. The one invariant is that minting stays **reactive** (§7): a link can only
ride out on a reply to an inbound message.

### Flow

```
 GROUP (WhatsApp)          NEXT.JS (:3000 public)          BOT (:8080 localhost)
 user types "dashboard"
   │ OnMessage (reactive) ───────────────────────────────▶ webauth.MintLink(group, member)
   │                                                          • random single-use nonce
   │                                                          • store {nonce→group,member,exp}
   │ ◀── Reply in group: "dashboard (15 min): …/auth?t=<nonce>"
 user taps link ─────────▶ GET /auth
                           POST localhost:8080/webauth/redeem {nonce} ─▶ webauth.Redeem
                                                                          • nonce live & unused?
                                                                          • mark used (atomic, single-use)
                                                                          • LIVE IsMember(group, member)?
                                                                          • yes → signed session token
                           ◀───────────────────────────────────────────── token (or 403)
                           Set-Cookie: session=<jwt> httpOnly; redirect
 every request: verify cookie locally (shared signing key) — no bot round-trip
 token expires hourly ───▶ POST /webauth/refresh {session} ─▶ webauth.Refresh
                                                                • signature valid?
                                                                • inside the 48h `abs` ceiling?
                                                                • LIVE IsMember again?
                           ◀───────────────────────────────── re-minted token (or 403)
 left the group ⇒ that refresh fails ⇒ logged out within RecheckInterval
```

### Why it's safe enough (and where it deliberately isn't gold-plated)

- The link is visible **only to group members** (it's an in-group message).
- **Single-use + short `LinkTTL`** — a later screenshot is dead.
- **Live membership at redeem** — a non-member never gets in.
- Data is **group-scoped** (shared family tasks / shared trip expenses), so the only question
  that matters is "current member: yes/no," not per-person identity.
- Residual risk (a member forwarding the live link to an outsider within its short window) is
  proportionate for a private group's read-only view.

### Config (three required secrets, four tunable knobs)

```go
webauth.Config{
    DashboardURL: "https://dash.example.com", // base for magic links
    APIToken:     "...",                       // bearer for /webauth/*
    SigningKey:   "...",                       // HMAC key for session tokens

    LinkTTL:         15 * time.Minute, // redemption window for the URL
    LinkSingleUse:   nil,              // nil == true; consumed on first redeem
    SessionTTL:      48 * time.Hour,   // absolute ceiling on one login
    RecheckInterval: 1 * time.Hour,    // token lifetime; removal revokes within this
}
```

**Locked defaults:** tight single-use link (15 min) + configurable long session (default 48h)
+ hourly membership recheck. The "revisit tomorrow" experience is delivered by the **session**,
not by keeping the link alive — the long-lived thing is an httpOnly cookie on the user's
device, never a bearer token in chat history. `LinkSingleUse` is a `*bool` so its zero value
means single-use: a config someone forgot to fill in must fail safe. Pointing it at `false`
gives a literally-reusable link, the documented looser choice.

`RecheckInterval` is the knob with a cost on both sides: it is simultaneously the revocation
lag (how long a removed member keeps access) and the rate of live WhatsApp group queries (one
per active user per interval). Shortening it tightens the first and multiplies the second.

### Boundary

- **`botkit/webauth` (Go)** owns all security logic: mint, single-use nonce store, redeem,
  live membership check, session-token signing.
- **Next.js dashboard (TS)** is thin: an `/auth` page that proxies the nonce to the bot and
  sets the cookie, plus per-request cookie validation.
- Fly routes public traffic to Next.js `:3000`; Next.js reaches the bot at `localhost:8080`,
  so `/webauth/redeem` is **never publicly reachable** — no brute-forcing.
- Shared config: `WEBAUTH_API_TOKEN` + `WEBAUTH_SIGNING_KEY` + `DASHBOARD_URL`. The two
  secrets are deliberately distinct — one leaked value must not both open the endpoints and
  forge sessions — and `webauth.New` refuses a config where they are equal.
- Membership keys on the member's **JID** (not raw phone), so LID-only participants still
  authenticate — `InboundMessage.SenderPhone` is empty for exactly those people. Every JID
  comparison requires User **and** Server to match, since phone numbers and LIDs are unrelated
  numeric namespaces that would otherwise collide (see `transport.sameJID`).
- Every rejection — expired nonce, replayed nonce, non-member, forged token — returns one
  identical opaque 403. A distinguishable error would turn the endpoint into a membership
  oracle. Reasons go to the bot's logs instead.
- A failed membership query **denies**. An unreachable WhatsApp is never an open door.

## 10. Inbound message model

Principle: **normalize the common 90%, expose raw for the long tail.** WhatsApp keeps adding
message types; an exhaustive enum goes stale. So `botkit` normalizes what every bot needs and
always exposes the untouched proto for the rest.

```go
type InboundMessage struct {
    ID, GroupID, SenderPhone, SenderName string
    SenderJID, GroupJID types.JID  // typed identities; prefer these for authorization
    Timestamp time.Time
    IsFromMe  bool

    Kind     Kind      // text|image|video|audio|document|sticker|contact|location|reaction|other
    Text     string    // plain text AND captions, unified
    Media    *Media    // image/video/audio/document/sticker
    Contacts []Contact
    Location *Location
    Reaction *Reaction
    Mentions []string  // phones
    Quoted   *Quoted   // the "previous message" reply context

    Raw *waE2E.Message  // escape hatch — always present; never blocked by a coverage gap
}
```

| WhatsApp type | Representation |
|---|---|
| `Conversation`, `ExtendedTextMessage` | `Text` |
| `Image/Video/Document(PDF/any)/Audio/Sticker` | `Media{Kind, MIME, Filename, Size, Duration, IsVoiceNote, IsViewOnce}` + caption → `Text` |
| `ContactMessage` / `ContactsArrayMessage` | `Contacts[]{DisplayName, VCard, Phones()}` |
| `LocationMessage` | `Location{Lat, Lng, Name, Address}` |
| `ReactionMessage` | `Reaction{Emoji, TargetID}` |
| `ContextInfo.QuotedMessage` | `Quoted{SenderPhone, Kind, Text}` |
| `ContextInfo.MentionedJID` | `Mentions[]` |
| polls, live location, business/interactive | not normalized → read `Raw` |

- **One download path:** every downloadable kind satisfies whatsmeow's `DownloadableMessage`,
  so `msg.Media.Download(ctx) ([]byte, error)` is the single fetch for all of them.
- **View-once** media is unwrapped transparently (`Media.IsViewOnce` set).
- **Voice note vs audio file:** both are `AudioMessage`; the PTT flag → `Media.IsVoiceNote`.
- **Media is opt-in:** `AcceptMedia` gates delivery; default off (nagger-style bots ignore it).
  No transcription/interpretation — bytes only.

## 11. Storage ownership

`botkit` owns only cross-cutting plumbing; the app owns all domain tables. SQLite stays each
app's source of truth.

- **Library-owned:** session-DB open (WAL/busy_timeout DSN); a generic **metadata KV** (for
  scheduler idempotency, `webauth` nonces, last-fired dates); **scheduler-run bookkeeping**.
- **App-owned:** everything domain (tasks, transactions, roster, join-request outcomes, …).

## 12. Pairing & lifecycle

- **Manual-only, always.** The bot never requests a pair code on its own — not at boot, not
  after `LoggedOut`, never. Auto-re-pairing is a documented Meta abuse trigger in *both* repos'
  post-mortems.
- Unpaired → the app idles and notifies an operator out-of-band; it does not crash or loop.
- `main` owns lifecycle: signal context, `Connect`, block. Ops API (`/pair` POST-only with a
  cooldown, `/status`, `/groups`) is the human's pairing entry point, bearer-token gated, and
  not publicly reachable.

## 13. Observability

- `telemetry.Init(ctx, service, version)` — OTLP-logs bootstrap, no-op when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is unset. Extracted verbatim.
- **The app calls `Init`**, not `bot.Run` — it owns its service name, its version, and the
  shutdown flush. `bot.Config` therefore carries no telemetry fields at all.
- **Event emission stays app-side** — nagger's ~20 typed events and AMIT's single `Audit`
  event are domain vocabularies, not framework concerns. `botkit` provides the transport and
  `redact` helpers; the app defines its events.

## 14. Build order

- **Phase 0** — new module + clean lifts: `redact`, `telemetry.Init`, `identity`,
  `store` session-open. Proves the module compiles and is importable.
- **Phase 1** — `transport` + `pairing` + ops API (from AMIT's `wa.Client` / `pairapi`).
- **Phase 2** — `bot` orchestrator + `OnMessage` dispatch + `InboundMessage` + guarded
  `Reply` + `gate`.
- **Phase 3** — `schedule` kernel + guarded action methods + `webauth`. ✅ done; `webauth`
  ships with a WhatsApp-free harness (`examples/webauth-dev`) that exercises the full
  mint → redeem → refresh → revoke loop against a fake group.
- **Then:** migrate AMIT onto `botkit` (lowest risk) → build travel-expenses on it.
- **Phase 2 (future):** Telegram transport behind the same handler seam; then nudnik.

## 15. Open / deferred

- Telegram transport (the handler API is already transport-neutral to accommodate it).
- Tier-2 `SendDM` and tier-3 proactive sending — only if a consumer needs them, and only
  through the guardrails in §7.
- Outbound media.
- ~~Whether `webauth`'s session token is a `botkit`-signed JWT the dashboard sets directly, or a
  claim the dashboard re-mints.~~ **Resolved:** a botkit-signed HS256 JWT the dashboard sets
  directly and verifies locally. It carries `exp` at `RecheckInterval` (1h) and an unmoving
  `abs` ceiling at `SessionTTL` (48h); `POST /webauth/refresh` re-checks live membership and
  re-mints against the same ceiling. A stateless 48h token could not have delivered the
  hourly revocation the section promises.
