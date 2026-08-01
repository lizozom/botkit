# webauth harness — the whole login flow, no WhatsApp

Exercise botkit's dashboard auth end to end without pairing a number or owning
a group. Two processes:

- **`devserver/`** — botkit's real `webauth` endpoints, with a fake group.
- **`web/`** — a Next.js dashboard: the `/auth` redeem route, the session
  middleware, a protected page, and a control panel standing in for WhatsApp.

Everything security-critical is the real implementation: same `webauth.Auth`,
same nonce store, same `/webauth/redeem` and `/webauth/refresh` handlers, same
token signing and verification. **Only the membership source is swapped** — an
in-memory set instead of a live WhatsApp query. That single substitution is what
lets you remove someone from "the group" and watch the dashboard log them out
seconds later.

The dashboard half is production-shaped. `web/app/auth/route.ts`,
`web/middleware.ts`, and `web/lib/token.ts` are close to what a real app ships;
`web/app/page.tsx` and the `/dev/*` routes are the fake-WhatsApp scaffolding
and have no production counterpart.

## Run it

```bash
# terminal 1 — the bot side
go run ./examples/webauth-dev/devserver

# terminal 2 — the dashboard
cd examples/webauth-dev/web
cp .env.local.example .env.local
npm install
npm run dev
```

Open http://localhost:3000.

If port 3000 is taken, Next will say so and use 3001 — restart the devserver
with `-dashboard http://localhost:3001` so minted links point at the right place.

## What to try

| Action | Expected | Why |
|---|---|---|
| Mint a link, tap it | lands on `/dashboard` | the happy path |
| Tap the same link twice | denied | single-use: the nonce is consumed atomically on first redeem |
| Mint, remove the member, *then* tap | denied | membership is checked live at redeem, not at mint |
| Log in, remove the member, wait one recheck | logged out | refresh re-checks membership; this is the revocation path |
| Log in as the LID-only member | works | authorization keys on JID, so people with no visible phone still get in |
| Edit the `session` cookie in devtools | denied | any tampering breaks the HMAC |

Waiting an hour for the recheck is impractical, so shrink the timings:

```bash
go run ./examples/webauth-dev/devserver -recheck 5s -link-ttl 10s -session-ttl 2m
```

Then log in, remove yourself from the group in the harness, and reload the
dashboard a few seconds later.

## Poking it directly

The devserver's `/dev/*` routes are plain HTTP:

```bash
BOT=http://127.0.0.1:8080
TOKEN="Authorization: Bearer dev-api-token-not-a-secret"

curl -s $BOT/dev/members                                              # who is in the group
LINK=$(curl -s -X POST $BOT/dev/mint \
  -d '{"member":"972500000001@s.whatsapp.net"}' | jq -r .link)        # "types dashboard"
curl -s -X POST $BOT/webauth/redeem -H "$TOKEN" \
  -d "{\"nonce\":\"${LINK##*t=}\"}"                                   # session token
curl -s -X POST $BOT/dev/members \
  -d '{"jid":"972500000001@s.whatsapp.net","action":"remove"}'        # leaves the group
```

Every rejection is an identical opaque 403 — expired, already used, and not-a-
member are indistinguishable from outside, so the endpoint cannot be probed to
learn who is in a group. Reasons go to the devserver's log.

## Not for production

The devserver ships fixed dev secrets, an unauthenticated control API that
mints a session for anyone who asks, and permissive CORS. It binds to localhost
and should stay there. The `web/` cookies set `secure: false` for plain-HTTP
localhost; turn that on anywhere else.

For the real setup, see [`../../docs/webauth.md`](../../docs/webauth.md).
