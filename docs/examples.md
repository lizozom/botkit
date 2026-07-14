# botkit — usage examples

Consumer-side code for every scenario `botkit` targets. All examples track the API in
[`../SPEC.md`](../SPEC.md). Illustrative, not compiled — signatures may shift during Phase 2/3.

- [The common shell](#the-common-shell) — the shape every bot shares
- [Example 1 — minimal text bot](#example-1--minimal-text-bot)
- [Example 2 — AMIT gatekeeper](#example-2--amit-gatekeeper)
- [Example 3 — travel-expenses](#example-3--travel-expenses)
- [Example 4 — the media grab-bag](#example-4--the-media-grab-bag)
- [Example 5 — the Next.js auth glue](#example-5--the-nextjs-auth-glue)

---

## The common shell

Every bot looks like this: build from config, register handlers, `Run`.

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfg := loadConfig() // app's own config struct

    b := bot.New(bot.Config{
        SessionDBPath: cfg.SessionDB,
        BotPhone:      cfg.Phone,          // manual pairing only
        ManagedGroups: cfg.ManagedGroups,  // fail-closed JID whitelist
        OTLP:          cfg.OTLP,
        OpsAddr:       ":8080",
        OpsToken:      cfg.PairToken,
    })

    // register handlers here …

    if err := b.Run(ctx); err != nil { // blocks until ctx cancelled
        log.Fatal(err)
    }
}
```

`Run` connects (or idles + notifies if unpaired — never auto-pairs), starts the ops API and
any scheduled jobs once online, dispatches events, and blocks. `main` owns the signal context.

---

## Example 1 — minimal text bot

Smallest possible app: reply to every message. Shows the reactive send.

```go
b.OnMessage(func(ctx context.Context, msg bot.InboundMessage) error {
    if msg.Text == "" {
        return nil
    }
    return msg.Reply(ctx, "you said: "+msg.Text)
})
```

---

## Example 2 — AMIT gatekeeper

No inbound messages. A scheduled sweep admits roster members; a daily audit flags strangers by
email. This is AMIT ported onto `botkit` — note how much hand-rolled machinery disappears.

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfg := loadConfig()
    roster := roster.New(cfg.RosterPath)          // app-owned
    mail := notifier.New(cfg.Resend)              // app-owned (email, out-of-band)

    b := bot.New(bot.Config{
        SessionDBPath: cfg.SessionDB,
        BotPhone:      cfg.Phone,
        ManagedGroups: cfg.ManagedGroups,
        OTLP:          cfg.OTLP,
        OpsAddr:       ":8080",
        OpsToken:      cfg.PairToken,
    })

    // Gatekeeper: poll pending join requests, admit anyone on the roster.
    b.OnSchedule("gatekeeper", schedule.EveryJittered(5*time.Minute, 2*time.Minute),
        func(ctx context.Context) error {
            for _, g := range b.ManagedGroups() {
                pending, err := b.PendingJoinRequests(ctx, g) // 429 backoff + LID→phone inside
                if err != nil {
                    return err
                }
                for _, req := range pending {
                    switch {
                    case req.Phone == "":
                        mail.Adminf("can't read number in %s, admit by hand", g)
                    case roster.Approved(req.Phone):
                        if err := b.ApproveJoin(ctx, g, req); err != nil { // humanized pause inside
                            return err
                        }
                    default:
                        mail.Adminf("stranger %s wants into %s", req.Phone, g)
                    }
                }
            }
            return nil
        })

    // Daily audit that never kicks — only reports members not on the roster.
    b.OnSchedule("audit", schedule.DailyAt(9, "Asia/Jerusalem"),
        func(ctx context.Context) error {
            for _, g := range b.ManagedGroups() {
                members, err := b.GroupMembers(ctx, g)
                if err != nil {
                    return err
                }
                var strangers []string
                for _, m := range members {
                    if !roster.Approved(m.Phone) {
                        strangers = append(strangers, m.Phone)
                    }
                }
                if len(strangers) > 0 {
                    mail.Adminf("%s: %d members not on the roster: %v", g, len(strangers), strangers)
                }
            }
            return nil
        })

    if err := b.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

The overlap guard, jitter, connected-gating, and daily-once idempotency are all handled by
`OnSchedule` — none of AMIT's hand-rolled `runLoop` / `sync.Once` / run-bookkeeping survives.

### Alerting on pairing loss

botkit never re-pairs itself, so an app that wants a human notified — instead of just a log
line — registers `OnPairingLost` before calling `Run`. It fires once whenever the session has
no usable pairing: there was never one at boot, or an existing one got logged out later
(device unlinked, or by WhatsApp). This replaces a hand-rolled poll of the ops API's
`/status` endpoint with the event straight from the source:

```go
b.OnPairingLost(func(reason string) {
    mail.Adminf("bot needs pairing (%s) — POST /pair, then enter the code on the phone", reason)
})
```

### Discovering groups (`AllGroups`)

`ManagedGroups()` only returns the configured whitelist. To discover JIDs in the first place
— or build an onboarding/admin view — call `AllGroups`, which returns every joined group
with name, admin status, member count, and community linkage:

```go
groups, err := b.AllGroups(ctx)
for _, g := range groups {
    fmt.Printf("%-30q %s admin=%v community=%v\n", g.Name, g.JID, g.IsAdmin, g.IsCommunity)
}
```

Like the other action methods, it's valid once the bot is connected (i.e. from within a
running handler or after `Run` has started) — it does not itself trigger pairing.

---

## Example 3 — travel-expenses

`OnMessage` with media on. A tour manager drops receipt photos/PDFs into the group; the bot
logs them and replies. Plus the in-group `dashboard` command that mints a `webauth` link
(reactive, no DM).

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfg := loadConfig()
    agent := agent.New(cfg.Anthropic) // app-owned LLM/tool-calling
    store := expenses.Open(cfg.DB)     // app-owned domain tables

    b := bot.New(bot.Config{
        SessionDBPath: cfg.SessionDB,
        BotPhone:      cfg.Phone,
        ManagedGroups: cfg.ManagedGroups,
        OTLP:          cfg.OTLP,
        OpsAddr:       ":8080",
        OpsToken:      cfg.PairToken,
        AcceptMedia:   true, // ← deliver images/PDFs to OnMessage
        WebAuth: &webauth.Config{
            LinkTTL:         15 * time.Minute,
            LinkSingleUse:   true,
            SessionTTL:      48 * time.Hour,
            RecheckInterval: 1 * time.Hour,
        },
    })

    b.OnMessage(func(ctx context.Context, msg bot.InboundMessage) error {
        // 1. Dashboard login command — reactive, in-group, no DM.
        if strings.EqualFold(strings.TrimSpace(msg.Text), "dashboard") {
            link := b.WebAuth().MintLink(ctx, msg.GroupID, msg.Member)
            return msg.Reply(ctx, "Dashboard (good for 15 min): "+link)
        }

        // 2. Invoice: an image or PDF, maybe with a caption.
        if msg.Media != nil && (msg.Media.Kind == bot.KindImage || msg.Media.MIME == "application/pdf") {
            blob, err := msg.Media.Download(ctx) // one path for any media kind
            if err != nil {
                return err
            }
            exp, err := agent.ExtractInvoice(ctx, blob, msg.Media.MIME, msg.Text /* caption */)
            if err != nil {
                return msg.Reply(ctx, "couldn't read that receipt — try a clearer photo?")
            }
            store.Add(msg.GroupID, msg.SenderName, exp)
            return msg.Reply(ctx, fmt.Sprintf("logged %s — %s", exp.Amount, exp.Merchant))
        }

        // 3. Plain text question → LLM.
        if msg.Text != "" {
            reply, err := agent.Handle(ctx, msg)
            if err != nil {
                return err
            }
            return msg.Reply(ctx, reply)
        }
        return nil
    })

    if err := b.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

---

## Example 4 — the media grab-bag

A handler that inspects any inbound kind. Note the `Raw` escape hatch for types `botkit`
doesn't normalize.

```go
b.OnMessage(func(ctx context.Context, msg bot.InboundMessage) error {
    switch msg.Kind {
    case bot.KindText:
        log.Printf("text: %q", msg.Text)

    case bot.KindImage, bot.KindVideo, bot.KindDocument, bot.KindSticker:
        log.Printf("%s %s (%d bytes, viewonce=%v) caption=%q",
            msg.Kind, msg.Media.MIME, msg.Media.Size, msg.Media.IsViewOnce, msg.Text)
        // blob, _ := msg.Media.Download(ctx)

    case bot.KindAudio:
        log.Printf("audio (voicenote=%v, %s)", msg.Media.IsVoiceNote, msg.Media.Duration)
        // botkit does nothing with it; download bytes if the app wants to

    case bot.KindContact:
        for _, c := range msg.Contacts {
            log.Printf("contact %s → %v", c.DisplayName, c.Phones()) // parsed from vCard
        }

    case bot.KindLocation:
        log.Printf("location %f,%f (%s)", msg.Location.Lat, msg.Location.Lng, msg.Location.Name)

    case bot.KindReaction:
        log.Printf("reaction %s on %s", msg.Reaction.Emoji, msg.Reaction.TargetID)

    default: // polls, live location, business/interactive …
        log.Printf("unnormalized type — inspecting raw proto: %T", msg.Raw)
    }

    // Reply context ("previous message") is orthogonal to Kind:
    if msg.Quoted != nil {
        log.Printf("in reply to %s: %q", msg.Quoted.SenderPhone, msg.Quoted.Text)
    }
    for _, phone := range msg.Mentions {
        log.Printf("mentioned: %s", phone)
    }
    return nil
})
```

---

## Example 5 — the Next.js auth glue

Everything security-critical lives in Go (`botkit/webauth`); this is all the dashboard writes.
See [`../SPEC.md` §9](../SPEC.md) for the full flow.

```ts
// app/auth/route.ts — redeem the nonce, set the cookie
export async function GET(req: Request) {
  const nonce = new URL(req.url).searchParams.get("t");
  const res = await fetch("http://localhost:8080/webauth/redeem", {
    method: "POST",
    headers: { authorization: `Bearer ${process.env.WEBAUTH_SECRET}` },
    body: JSON.stringify({ nonce }),
  });
  if (!res.ok) return Response.redirect("/denied"); // expired, used, or not a member
  const { session } = await res.json();
  const out = Response.redirect("/dashboard");
  out.headers.set("set-cookie",
    `session=${session}; HttpOnly; Secure; Path=/; Max-Age=${48 * 3600}`);
  return out;
}
```

```ts
// middleware.ts — validate cookie locally each request (no bot round-trip)
export function middleware(req: NextRequest) {
  const token = req.cookies.get("session")?.value;
  if (!token || !verifyJWT(token, process.env.WEBAUTH_SECRET)) {
    return NextResponse.redirect(new URL("/denied", req.url));
  }
  return NextResponse.next();
}
```

---

**API validation signal:** AMIT collapses to two scheduled jobs; travel-expenses is one
`OnMessage` handler. The whatsmeow/pairing/media/auth complexity all stays inside `botkit`.
