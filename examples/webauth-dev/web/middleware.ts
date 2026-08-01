// Per-request session check for /dashboard.
//
// The common path is local: verify the cookie with the shared key, no bot
// round-trip. Once an hour the token expires and this refreshes it — the moment
// membership is re-checked, and what makes removal actually log someone out.

import { NextRequest, NextResponse } from "next/server";
import { nowSeconds, verifyToken } from "./lib/token";

const BOT = process.env.BOT_URL ?? "http://127.0.0.1:8080";
const SESSION_MAX_AGE = 48 * 3600;

export const config = { matcher: ["/dashboard/:path*"] };

export async function middleware(req: NextRequest) {
  const denied = NextResponse.redirect(new URL("/denied", req.url));
  const token = req.cookies.get("session")?.value;
  if (!token) return denied;

  const claims = await verifyToken(token, process.env.WEBAUTH_SIGNING_KEY!);
  if (!claims) return denied; // forged, tampered, or signed with another key

  // Still inside the hour: nothing to do.
  if (claims.exp > nowSeconds()) return NextResponse.next();

  // The hour is up: re-check membership and re-mint. A rejection means they
  // left the group or hit the 48h ceiling — either way, logged out.
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
    httpOnly: true,
    secure: false, // harness runs on http://localhost; enable off-localhost
    sameSite: "lax",
    path: "/",
    maxAge: SESSION_MAX_AGE,
  });
  return out;
}
