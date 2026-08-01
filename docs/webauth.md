# webauth — dashboard login gated on group membership

> **Status: implemented.** Everything below is live in the `webauth` package.
> To try the whole flow without pairing a WhatsApp number, run the harness in
> [`../examples/webauth-dev`](../examples/webauth-dev) — real auth, fake group.

Your bot has a companion web dashboard. You want people in the WhatsApp group to
be able to open it, and nobody else. You do not want to run a signup form, store
passwords, maintain an allowlist, or send anyone a DM.

`webauth` gives you that: someone types `dashboard` in the group, the bot replies
with a link, they tap it, they're in. Authorization is **live group membership** —
the only question ever asked is "is this person in the group right now?"

Full design rationale lives in [`../SPEC.md` §9](../SPEC.md). This page is the
setup guide.

---

## How it works

```
 GROUP (WhatsApp)         NEXT.JS :3000 (public)        BOT :8080 (localhost only)
 user types "dashboard"
   │ OnGroupMessage ──────────────────────────────────▶ MintLink(group, sender)
   │                                                       • random single-use nonce
   │                                                       • stored with 15 min expiry
   │ ◀── bot replies in group: ".../auth?t=<nonce>"
 user taps link ─────────▶ GET /auth
                           POST /webauth/redeem {nonce} ──▶ Redeem
                                                              • nonce live & unused?
                                                              • consume it (atomic)
                                                              • LIVE IsMember(group, sender)?
                           ◀────────────────────────────────── 1h session token, or 403
                           Set-Cookie (httpOnly), redirect
 each request: verify cookie locally — no bot round-trip
 token expires hourly ───▶ POST /webauth/refresh ────────────▶ re-check membership, re-mint
                                                              (until the 48h ceiling)
```

Four properties do the security work:

1. The link is only ever posted **inside the group**, so only members see it.
2. It is **single-use** and expires in **15 minutes**.
3. Membership is checked **live at redeem**, not at mint. A stolen nonce is
   useless to a non-member.
4. The session token lives **one hour**. Renewing it re-checks membership, so
   removing someone from the group logs them out within the hour.

**Accepted residual risk:** a member can forward a live link to an outsider
inside the 15-minute window. The design accepts this for a private group's
read-only dashboard. If your dashboard lets people *write* data, this trade-off
needs revisiting before you ship.

---

## Setup

### 1. Generate two secrets

They do different jobs. Do not reuse one value for both — a single leaked string
should not grant both "call the bot's API" and "forge any session token." A
config where they are equal is rejected at `bot.New`, not at first login.

```bash
openssl rand -hex 32   # WEBAUTH_API_TOKEN    — bearer token for the bot's endpoints
openssl rand -hex 32   # WEBAUTH_SIGNING_KEY  — HMAC key for session tokens
```

Both processes need both values:

| Variable | Bot | Dashboard | Purpose |
|---|:--:|:--:|---|
| `WEBAUTH_API_TOKEN` | ✓ | ✓ | Dashboard authenticates to `/webauth/*` |
| `WEBAUTH_SIGNING_KEY` | ✓ | ✓ | Bot signs session tokens, dashboard verifies them |
| `DASHBOARD_URL` | ✓ | — | Base URL the bot builds magic links from |

### 2. Configure the bot

```go
import "github.com/lizozom/botkit/webauth"

b, err := bot.New(bot.Config{
	SessionDBPath: "whatsapp_session.db",
	BotPhone:      os.Getenv("BOT_PHONE"),
	ManagedGroups: []string{"12036...@g.us"},
	OpsAddr:       ":8080",
	OpsToken:      os.Getenv("PAIR_TOKEN"),

	WebAuth: &webauth.Config{
		DashboardURL: os.Getenv("DASHBOARD_URL"),
		APIToken:     os.Getenv("WEBAUTH_API_TOKEN"),
		SigningKey:   os.Getenv("WEBAUTH_SIGNING_KEY"),
		// timing knobs left at their defaults — see the table below
	},
})
```

Setting `WebAuth` mounts `/webauth/redeem` and `/webauth/refresh` on the
existing ops port (`OpsAddr`), alongside `/pair`, `/status`, and `/groups`.

### 3. Add the login command

```go
b.OnGroupMessage(func(ctx context.Context, msg bot.InboundMessage) error {
	if strings.EqualFold(strings.TrimSpace(msg.Text), "dashboard") {
		link, err := b.WebAuth().MintLink(ctx, msg.GroupJID, msg.SenderJID)
		if err != nil {
			return err
		}
		return msg.Reply(ctx, "Dashboard (good for 15 minutes): "+link)
	}
	return nil
})
```

That is the entire bot-side integration. Nonce generation, storage, expiry,
single-use enforcement, and the membership check all live inside `webauth`.

Pass the typed `msg.GroupJID` / `msg.SenderJID`, not `msg.GroupID` /
`msg.SenderPhone`. Membership keys on the JID so that LID-only participants —
people whose phone number the bot cannot see — can still log in; `SenderPhone`
is empty for exactly those people.

### 4. Add the redeem route

```ts
// app/auth/route.ts — exchange the nonce for a session cookie
import { NextResponse } from "next/server";

const BOT = process.env.BOT_URL ?? "http://127.0.0.1:8080";

export async function GET(req: Request) {
  const nonce = new URL(req.url).searchParams.get("t");
  const denied = new URL("/denied", req.url);
  if (!nonce) return NextResponse.redirect(denied);

  let res: Response;
  try {
    res = await fetch(`${BOT}/webauth/redeem`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${process.env.WEBAUTH_API_TOKEN}`,
      },
      body: JSON.stringify({ nonce }),
      cache: "no-store",
    });
  } catch {
    return NextResponse.redirect(denied); // bot unreachable — fail closed
  }

  // One 403 covers expired, already-redeemed, and not-a-member. The bot does
  // not say which, so neither can you.
  if (!res.ok) return NextResponse.redirect(denied);

  const { session } = (await res.json()) as { session: string };

  const out = NextResponse.redirect(new URL("/dashboard", req.url));
  out.cookies.set("session", session, {
    httpOnly: true,
    secure: true,
    sameSite: "lax",
    path: "/",
    // The 48h ceiling. The token inside expires hourly and refreshes in place.
    maxAge: 48 * 3600,
  });
  return out;
}
```

### 5. Add the per-request check

```ts
// middleware.ts — verify locally, refresh hourly
import { NextRequest, NextResponse } from "next/server";
import { verifyToken } from "./lib/token"; // HS256 via Web Crypto — see the harness

const BOT = process.env.BOT_URL ?? "http://127.0.0.1:8080";

export const config = { matcher: ["/dashboard/:path*"] };

export async function middleware(req: NextRequest) {
  const denied = NextResponse.redirect(new URL("/denied", req.url));
  const token = req.cookies.get("session")?.value;
  if (!token) return denied;

  const claims = await verifyToken(token, process.env.WEBAUTH_SIGNING_KEY!);
  if (!claims) return denied; // forged, tampered, or signed with another key

  // Inside the hour — the common path, no bot round-trip.
  if (claims.exp > Math.floor(Date.now() / 1000)) return NextResponse.next();

  // The hour is up: re-check membership and re-mint. A rejection means they
  // left the group or hit the 48h ceiling.
  let res: Response;
  try {
    res = await fetch(`${BOT}/webauth/refresh`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        authorization: `Bearer ${process.env.WEBAUTH_API_TOKEN}`,
      },
      body: JSON.stringify({ session: token }),
      cache: "no-store",
    });
  } catch {
    return denied; // bot unreachable — fail closed
  }
  if (!res.ok) return denied;

  const { session } = (await res.json()) as { session: string };
  const out = NextResponse.next();
  out.cookies.set("session", session, {
    httpOnly: true, secure: true, sameSite: "lax", path: "/", maxAge: 48 * 3600,
  });
  return out;
}
```

Both files above are lifted from
[`../examples/webauth-dev/web`](../examples/webauth-dev), where they run against
the real endpoints — including `lib/token.ts`, a ~40-line HS256 verifier built
on Web Crypto so the middleware needs no JWT dependency. Use a maintained
library (`jose`) if you would rather not own that code.

The harness sets `secure: false` because it serves plain HTTP on localhost.
Anywhere else, keep it `true` as shown here.

### 6. Check the network layout

The bot's ops port must be reachable from the dashboard process and from
**nowhere else**. On Fly: route public traffic to the Next.js app on `:3000`
and leave `:8080` unpublished, so Next.js reaches it over `localhost`.

This is not a nice-to-have. `/webauth/redeem` is a nonce-guessing surface; the
bearer token and the 15-minute TTL are the second and third lines of defense,
and the first one is that the endpoint has no route from the internet.

Verify from outside the machine before you ship:

```bash
curl -m 5 https://your-app.example.com:8080/healthz   # MUST fail to connect
```

---

## Reference

### Config

```go
webauth.Config{
	DashboardURL: "https://dash.example.com", // required — base for magic links
	APIToken:     "...",                       // required — bearer for /webauth/*
	SigningKey:   "...",                       // required — HMAC key for tokens

	LinkTTL:         15 * time.Minute, // how long a magic link stays redeemable
	LinkSingleUse:   nil,              // nil means true; set &false to opt out
	SessionTTL:      48 * time.Hour,   // absolute ceiling; re-login after this
	RecheckInterval: 1 * time.Hour,    // token lifetime = revocation lag
}
```

The three required fields have no defaults; the four timing knobs are shown at
their defaults, so an empty `webauth.Config{}` with only secrets filled in
behaves exactly as documented here.

Two knobs worth understanding before you change them:

**`RecheckInterval`** is your revocation lag. It is how long someone keeps
dashboard access after being removed from the group, and it is also how often
each active user costs you one WhatsApp group-info query. Lowering it to
`5 * time.Minute` tightens revocation and multiplies that query rate by twelve.

**`LinkSingleUse`** is a `*bool` so that the zero value means "single-use"
rather than "reusable" — a config someone forgot to fill in must fail safe.
Pointing it at `false` gives a link that stays valid until `LinkTTL` expires,
for consumers who explicitly want something shareable. It costs you the
"a screenshot of the chat is dead on arrival" property, so opt in deliberately.

### `MintLink`

```go
func (a *Auth) MintLink(ctx context.Context, group, member types.JID) (string, error)
```

Returns a full URL. Call it from a message handler and reply with the result.
It does **not** verify that `member` is in `group` — the check that matters
happens live at redeem, and duplicating it at mint would only be a courtesy.

Each mint also sweeps nonces older than `LinkTTL` out of the KV, so unredeemed
links do not accumulate. The sweep runs on SQLite's one-second timestamp
granularity; per-link expiry is checked exactly, from the record itself.

### Other methods

```go
func (a *Auth) Redeem(ctx context.Context, nonce string) (token string, err error)
func (a *Auth) Refresh(ctx context.Context, token string) (string, error)
func (a *Auth) Verify(token string) (Claims, error)  // signature + expiry, no membership query
func (a *Auth) TTL() time.Duration                   // lifetime of a fresh token
func (a *Auth) Handler() http.Handler                // the /webauth/* routes
```

The endpoints wrap `Redeem` and `Refresh`; call them directly only if you are
serving the dashboard from the same process. Every failure returns `ErrDenied`
— one sentinel for all of them, deliberately.

### Endpoints

Both require `Authorization: Bearer <WEBAUTH_API_TOKEN>` and both are mounted
on `OpsAddr`.

```
POST /webauth/redeem     {"nonce": "<nonce>"}
  200 {"session": "<token>", "expires_in": 3600}
  403 — nonce expired, already used, or member no longer in the group

POST /webauth/refresh    {"session": "<token>"}
  200 {"session": "<token>", "expires_in": 3600}
  403 — token invalid, past the 48h ceiling, or member no longer in the group
```

Both return an identical opaque 403 for every failure mode. This is deliberate:
distinguishing "that nonce expired" from "you are not in the group" hands a
probing attacker a membership oracle. When you need to know which case you hit,
read the bot's logs.

### Session token

HMAC-SHA256 over these claims:

| Claim | Meaning |
|---|---|
| `grp` | group JID the session is scoped to |
| `mbr` | member JID |
| `iat` | issued at |
| `exp` | expires — `iat + RecheckInterval` (1h) |
| `abs` | absolute ceiling — first login + `SessionTTL` (48h) |

`abs` is what stops refresh from being an infinite renewal. It rides through
every refresh unchanged; once it passes, the user logs in again from the group.

Sessions are **group-scoped**. A token proves "a current member of group X,"
so a bot managing several groups must key its dashboard data on `grp` — never
assume one session sees everything.

---

## Troubleshooting

**Every link says "denied."** Check `WEBAUTH_SIGNING_KEY` is byte-identical in
both processes. Different values fail as an ordinary auth failure, not an
obvious startup error.

**Redeem returns 403 for a person who is definitely in the group.** They are
probably LID-only. Confirm the handler passes `msg.SenderJID` and not
`msg.SenderPhone` — the phone is empty for these participants.

**Link works once, then a second tap denies.** Working as designed —
`LinkSingleUse`. Refreshing the `/auth` page counts as that second tap. Type
`dashboard` again for a new link.

**Users get logged out well before 48h.** Refresh is failing and the middleware
falls through to `/denied`. Check the dashboard can reach the bot's ops port,
and that `WEBAUTH_API_TOKEN` matches on both sides.

**Nonces in your access logs.** Expected — the nonce rides in the query string.
It is single-use and 15-minute-lived, so a logged one is near-certainly already
dead, but if your log retention is long and widely readable, scrub the `t`
parameter at the ingest layer.

---

## Try it without WhatsApp

[`../examples/webauth-dev`](../examples/webauth-dev) runs this whole flow with a
fake group: mint a link, redeem it, replay it, remove the member and watch the
session die. The auth is the real implementation — only the membership lookup is
swapped for an in-memory set you control from a control panel.

```bash
go run ./examples/webauth-dev/devserver
cd examples/webauth-dev/web && cp .env.local.example .env.local && npm install && npm run dev
```
