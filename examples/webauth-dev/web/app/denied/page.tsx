// Every rejection lands here — expired, already redeemed, not a member,
// tampered cookie, past the ceiling — and says nothing about which. The bot
// returns one opaque 403 for all of them so the endpoints cannot be probed;
// a page that spelled out the reason would give that away again.

export default function Denied() {
  return (
    <main>
      <h1>Denied</h1>
      <p className="sub">
        That link or session is not valid. Ask in the group for a new one.
      </p>
      <p>
        <a href="/">← back to the harness</a>
      </p>
      <style>{`
        :root { color-scheme: light dark; }
        main { max-width: 34rem; margin: 5rem auto; padding: 0 1.25rem;
               font: 15px/1.6 ui-sans-serif, system-ui, sans-serif; }
        h1 { font-size: 1.5rem; margin-bottom: .25rem; }
        .sub { opacity: .7; margin-top: 0; }
        a { color: inherit; }
      `}</style>
    </main>
  );
}
