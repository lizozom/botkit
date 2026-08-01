// HS256 verification via Web Crypto, so the harness needs no JWT dependency and
// runs in the edge middleware runtime.
//
// A real dashboard would use a maintained library (jose, jsonwebtoken). This is
// deliberately small so the mechanism stays visible: the token is trusted only
// when its signature is byte-identical to what the shared key produces, and the
// header's "alg" is never consulted to decide how to verify.

export type Claims = {
  grp: string; // group JID the session is scoped to
  mbr: string; // member JID
  iat: number;
  exp: number; // issued + RecheckInterval — refresh past this
  abs: number; // absolute ceiling; never extended by a refresh
};

// Both helpers return a Uint8Array explicitly backed by an ArrayBuffer:
// Web Crypto's BufferSource excludes SharedArrayBuffer-backed views, which is
// what a bare Uint8Array widens to under TypeScript 5.7+.

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
 * Verify a botkit session token. Returns its claims, or null if the signature
 * is wrong or the token is malformed.
 *
 * Expiry is NOT checked here — the caller decides, because the refresh path
 * must accept a token whose `exp` has already passed.
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
