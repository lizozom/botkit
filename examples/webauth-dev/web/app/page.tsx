"use client";

// The harness: a stand-in for the WhatsApp group. "Type dashboard" calls
// /dev/mint, which is what a bot's OnGroupMessage handler would do. Add/Remove
// edits the membership the real IsMember would read from WhatsApp.

import { useCallback, useEffect, useState } from "react";

const BOT = process.env.NEXT_PUBLIC_BOT_URL ?? "http://127.0.0.1:8080";

type Member = { jid: string; name: string };

export default function Harness() {
  const [members, setMembers] = useState<Member[]>([]);
  const [group, setGroup] = useState("");
  const [links, setLinks] = useState<Record<string, string>>({});
  const [newJid, setNewJid] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const res = await fetch(`${BOT}/dev/members`, { cache: "no-store" });
      const data = await res.json();
      setMembers(data.members ?? []);
      setGroup(data.group ?? "");
      setError("");
    } catch {
      setError(`Cannot reach the devserver at ${BOT}. Is it running?`);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function mint(jid: string) {
    const res = await fetch(`${BOT}/dev/mint`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ member: jid }),
    });
    const data = await res.json();
    setLinks((prev) => ({ ...prev, [jid]: data.link }));
  }

  async function edit(jid: string, action: "add" | "remove", name?: string) {
    await fetch(`${BOT}/dev/members`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jid, name, action }),
    });
    await load();
  }

  return (
    <main>
      <h1>webauth harness</h1>
      <p className="sub">
        Real botkit auth, fake WhatsApp group. Mint a link, tap it, then remove
        the person from the group and watch the session die.
      </p>

      {error && <p className="error">{error}</p>}

      <section>
        <h2>Group</h2>
        <code className="jid">{group || "…"}</code>

        <table>
          <thead>
            <tr>
              <th>Member</th>
              <th>JID</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.jid}>
                <td>{m.name}</td>
                <td>
                  <code className="jid">{m.jid}</code>
                </td>
                <td className="actions">
                  <button onClick={() => mint(m.jid)}>Type “dashboard”</button>
                  <button className="danger" onClick={() => edit(m.jid, "remove")}>
                    Remove from group
                  </button>
                </td>
              </tr>
            ))}
            {members.length === 0 && (
              <tr>
                <td colSpan={3} className="empty">
                  Nobody in the group. Nothing can log in.
                </td>
              </tr>
            )}
          </tbody>
        </table>

        <div className="add">
          <input
            value={newJid}
            placeholder="972500000000@s.whatsapp.net"
            onChange={(e) => setNewJid(e.target.value)}
          />
          <button
            onClick={() => {
              if (newJid.trim()) {
                void edit(newJid.trim(), "add", newJid.trim());
                setNewJid("");
              }
            }}
          >
            Add to group
          </button>
        </div>
      </section>

      <section>
        <h2>Minted links</h2>
        <p className="sub">
          Each is single-use and expires in 15 minutes — the same link the bot
          would have replied with in the group.
        </p>
        {Object.entries(links).length === 0 && (
          <p className="empty">None yet.</p>
        )}
        <ul>
          {Object.entries(links).map(([jid, link]) => (
            <li key={jid}>
              <div className="who">{jid}</div>
              <a href={link}>{link}</a>
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h2>Things worth trying</h2>
        <ol>
          <li>Mint a link and tap it — you land on /dashboard.</li>
          <li>
            Tap the same link again — denied. It was consumed on first use.
          </li>
          <li>
            Mint a link, remove the person from the group, <em>then</em> tap it
            — denied. Membership is checked at redeem, not at mint.
          </li>
          <li>
            Add someone, mint, log in, then remove them and wait for the token
            to expire — the next request refreshes, fails the re-check, and logs
            them out. Restart the devserver with a short{" "}
            <code>RecheckInterval</code> to see it without waiting an hour.
          </li>
          <li>
            Log in, then edit the cookie in devtools — any tampering breaks the
            signature and denies.
          </li>
        </ol>
      </section>

      <p>
        <a href="/dashboard">Open the dashboard →</a>
      </p>

      <style>{`
        :root { color-scheme: light dark; }
        main { max-width: 46rem; margin: 3rem auto; padding: 0 1.25rem;
               font: 15px/1.6 ui-sans-serif, system-ui, sans-serif; }
        h1 { font-size: 1.5rem; margin-bottom: .25rem; }
        h2 { font-size: 1rem; margin: 2rem 0 .5rem; text-transform: uppercase;
             letter-spacing: .06em; opacity: .6; }
        .sub { opacity: .7; margin-top: 0; }
        .error { padding: .75rem 1rem; border-radius: 8px;
                 background: color-mix(in srgb, crimson 12%, transparent);
                 border: 1px solid color-mix(in srgb, crimson 40%, transparent); }
        table { width: 100%; border-collapse: collapse; margin-top: .75rem; }
        th { text-align: left; font-size: .75rem; text-transform: uppercase;
             letter-spacing: .05em; opacity: .5; padding-bottom: .4rem; }
        td { padding: .55rem 0; border-top: 1px solid rgba(128,128,128,.25);
             vertical-align: middle; }
        .jid { font-size: .8rem; opacity: .75; word-break: break-all; }
        .actions { text-align: right; white-space: nowrap; }
        button { font: inherit; font-size: .85rem; padding: .3rem .7rem;
                 margin-left: .4rem; border-radius: 6px; cursor: pointer;
                 border: 1px solid rgba(128,128,128,.4); background: transparent;
                 color: inherit; }
        button:hover { background: rgba(128,128,128,.12); }
        button.danger { border-color: color-mix(in srgb, crimson 45%, transparent); }
        .add { margin-top: 1rem; display: flex; gap: .4rem; }
        .add input { flex: 1; font: inherit; font-size: .85rem; padding: .35rem .6rem;
                     border-radius: 6px; border: 1px solid rgba(128,128,128,.4);
                     background: transparent; color: inherit; }
        .empty { opacity: .5; padding: .75rem 0; }
        ul { list-style: none; padding: 0; }
        li { padding: .6rem 0; border-top: 1px solid rgba(128,128,128,.25); }
        .who { font-size: .75rem; opacity: .55; }
        a { color: inherit; }
        ol { padding-left: 1.2rem; } ol li { border: 0; }
        code { font-size: .85em; }
      `}</style>
    </main>
  );
}
