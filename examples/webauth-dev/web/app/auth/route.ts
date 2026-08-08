// GET /auth?t=<nonce> — the magic link's landing route, and the whole
// dashboard-side redeem: hand the nonce to the bot, set what comes back as an
// httpOnly cookie. The dashboard never validates the nonce or looks at group
// membership. Copy into a real app almost verbatim.

import { NextResponse } from "next/server";

const BOT = process.env.BOT_URL ?? "http://127.0.0.1:8080";
const SESSION_MAX_AGE = 48 * 3600; // matches SessionTTL — the absolute ceiling

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
    // Bot unreachable — a down membership check is never an open door.
    return NextResponse.redirect(denied);
  }

  // One 403 covers expired, already-redeemed, and not-a-member. The bot does
  // not say which, so neither can we.
  if (!res.ok) return NextResponse.redirect(denied);

  const { session } = (await res.json()) as { session: string };

  const out = NextResponse.redirect(new URL("/dashboard", req.url));
  out.cookies.set("session", session, {
    httpOnly: true,
    // Off only because the harness is http://localhost. On anywhere else.
    secure: false,
    sameSite: "lax",
    path: "/",
    maxAge: SESSION_MAX_AGE,
  });
  return out;
}
