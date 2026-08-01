// The protected page. Reaching it at all means middleware.ts accepted the
// cookie, so this only needs to read the claims to know who is looking.
//
// Note the session is group-scoped: a real dashboard must filter its data by
// claims.grp rather than assume one login sees everything.

import { cookies } from "next/headers";
import { verifyToken, nowSeconds } from "../../lib/token";

export const dynamic = "force-dynamic";

export default async function Dashboard() {
  const token = (await cookies()).get("session")?.value;
  const claims = token
    ? await verifyToken(token, process.env.WEBAUTH_SIGNING_KEY!)
    : null;

  if (!claims) {
    // Unreachable in practice — middleware would have redirected already.
    return <main>No session.</main>;
  }

  const secs = (n: number) => Math.max(0, n - nowSeconds());
  const fmt = (n: number) =>
    n > 3600 ? `${Math.floor(n / 3600)}h ${Math.floor((n % 3600) / 60)}m` : `${Math.floor(n / 60)}m ${n % 60}s`;

  return (
    <main>
      <h1>You are in ✅</h1>
      <p className="sub">
        This page is gated on live WhatsApp group membership. Nobody signed up,
        nobody has a password.
      </p>

      <dl>
        <dt>Member</dt>
        <dd>
          <code>{claims.mbr}</code>
        </dd>
        <dt>Group</dt>
        <dd>
          <code>{claims.grp}</code>
        </dd>
        <dt>Token expires in</dt>
        <dd>
          {fmt(secs(claims.exp))}{" "}
          <span className="note">
            — then a refresh re-checks membership
          </span>
        </dd>
        <dt>Session ceiling in</dt>
        <dd>
          {fmt(secs(claims.abs))}{" "}
          <span className="note">— refresh cannot extend past this</span>
        </dd>
      </dl>

      <p>
        <a href="/">← back to the harness</a>
      </p>

      <style>{`
        :root { color-scheme: light dark; }
        main { max-width: 40rem; margin: 3rem auto; padding: 0 1.25rem;
               font: 15px/1.6 ui-sans-serif, system-ui, sans-serif; }
        h1 { font-size: 1.5rem; margin-bottom: .25rem; }
        .sub { opacity: .7; margin-top: 0; }
        dl { margin: 2rem 0; }
        dt { font-size: .7rem; text-transform: uppercase; letter-spacing: .06em;
             opacity: .5; margin-top: 1rem; }
        dd { margin: .15rem 0 0; }
        code { font-size: .85em; word-break: break-all; }
        .note { opacity: .55; font-size: .85em; }
        a { color: inherit; }
      `}</style>
    </main>
  );
}
