// HS256 verification via Web Crypto: no JWT dependency, runs in edge middleware.
//
// A real dashboard would use jose. This stays small so the mechanism is visible
// — the token is trusted only when its signature matches what the shared key
// produces, and the header's "alg" never decides how to verify.

export type Claims = {
  grp: string; // group JID the session is scoped to
  mbr: string; // member JID
  iat: number;
  exp: number; // issued + RecheckInterval — refresh past this
  abs: number; // absolute ceiling; never extended by a refresh
};

// Both helpers return a Uint8Array explicitly backed by an ArrayBuffer, which
// Web Crypto's BufferSource requires under TypeScript 5.7+.

function b64urlDecode(input: string): Uint8Array<ArrayBuffer> {
  const pad = input.length % 4 === 0 ? "" : "=".repeat(4 - (input.length % 4));
  const b64 = input.replace(/-/g, "+").replace(/_/g, "/") + pad;
  const bin = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function utf8(s: string): Uint8Array<ArrayBuffer> {
  const src = new TextEncoder().encode(s);
  const out = new Uint8Array(new ArrayBuffer(src.length));
  out.set(src);
  return out;
}

/**
 * Verify a botkit session token. Returns its claims, or null if malformed or
 * wrongly signed. Expiry is not checked here — the refresh path must accept a
 * token whose `exp` has passed.
 */
export async function verifyToken(
  token: string,
  signingKey: string,
): Promise<Claims | null> {
  const parts = token.split(".");
  if (parts.length !== 3) return null;

  const key = await crypto.subtle.importKey(
    "raw",
    utf8(signingKey),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["verify"],
  );

  let ok = false;
  try {
    ok = await crypto.subtle.verify(
      "HMAC",
      key,
      b64urlDecode(parts[2]),
      utf8(`${parts[0]}.${parts[1]}`),
    );
  } catch {
    return null; // unparseable signature segment
  }
  if (!ok) return null;

  try {
    const claims = JSON.parse(
      new TextDecoder().decode(b64urlDecode(parts[1])),
    ) as Claims;
    if (!claims.grp || !claims.mbr) return null;
    return claims;
  } catch {
    return null;
  }
}

export const nowSeconds = () => Math.floor(Date.now() / 1000);
