// Package webauth implements membership-gated magic-link auth for a companion
// dashboard: MintLink (called from a message handler, replied in-group), a
// single-use nonce store, Redeem (live IsMember check -> signed session token),
// Refresh (re-check + re-mint), and the /webauth/* endpoints.
//
// See ../SPEC.md §9 for the design and ../docs/webauth.md for consumer setup.
//
// The whole security model rests on three properties, in descending order of
// importance: membership is verified live at redeem and at every refresh, never
// trusted from the link; the nonce is consumed atomically so a link redeems
// exactly once; and every failure looks identical to the caller, so the
// endpoints cannot be used as a membership oracle.
package webauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lizozom/botkit/store"
	"go.mau.fi/whatsmeow/types"
)

// noncePrefix namespaces magic-link nonces inside the shared botkit KV.
const noncePrefix = "webauth:nonce:"

// ErrDenied is the single error every failure path returns: expired nonce,
// already-redeemed nonce, forged token, expired session, member no longer in
// the group. Callers cannot tell these apart, and that is the point — a
// distinguishable error would let anyone holding a dead nonce probe whether a
// given person is in a group. Operators get the real reason from the logs.
var ErrDenied = errors.New("webauth: denied")

// Membership is the live authorization check webauth depends on. *bot.Bot's
// transport satisfies it; tests fake it.
type Membership interface {
	IsMember(ctx context.Context, group, member types.JID) (bool, error)
}

// Auth mints and redeems dashboard magic links. Build it with New.
type Auth struct {
	cfg   Config
	kv    *store.KV
	mem   Membership
	clock func() time.Time // injectable for tests
}

// nonceRecord is what a pending magic link stores under its nonce.
type nonceRecord struct {
	Group   string `json:"grp"`
	Member  string `json:"mbr"`
	Expires int64  `json:"exp"`
}

// New validates cfg and returns an Auth. It fails fast on a missing secret
// rather than deferring to the first login attempt.
func New(cfg Config, kv *store.KV, mem Membership) (*Auth, error) {
	full, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if kv == nil {
		return nil, errors.New("webauth: nil KV")
	}
	if mem == nil {
		return nil, errors.New("webauth: nil Membership — there would be nothing to authorize against")
	}
	return &Auth{cfg: full, kv: kv, mem: mem, clock: time.Now}, nil
}

// MintLink issues a magic link authorizing member to open the dashboard for
// group, and returns the full URL to reply with in-group.
//
// It deliberately does not check that member is in group: the check that counts
// runs live at redeem, and a second one here would only add a WhatsApp query
// per mint while proving nothing about the state at redeem time.
func (a *Auth) MintLink(ctx context.Context, group, member types.JID) (string, error) {
	if group.IsEmpty() || member.IsEmpty() || member.User == "" {
		return "", errors.New("webauth: MintLink needs both a group and a member JID")
	}

	// Piggyback nonce GC on mint: links are minted far more often than they are
	// redeemed, so without this the unredeemed ones accumulate forever. Old
	// enough to be past LinkTTL means old enough to be unusable.
	if n, err := a.kv.DeleteExpired(ctx, noncePrefix, a.cfg.LinkTTL); err != nil {
		slog.Warn("webauth: nonce sweep failed", slog.String("err", err.Error()))
	} else if n > 0 {
		slog.Debug("webauth: swept expired nonces", slog.Int64("count", n))
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("webauth: generate nonce: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)

	rec, err := json.Marshal(nonceRecord{
		Group:   group.String(),
		Member:  member.String(),
		Expires: a.clock().Add(a.cfg.LinkTTL).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("webauth: encode nonce record: %w", err)
	}
	if err := a.kv.Set(ctx, noncePrefix+nonce, string(rec)); err != nil {
		return "", fmt.Errorf("webauth: store nonce: %w", err)
	}
	return a.cfg.DashboardURL + "/auth?t=" + nonce, nil
}

// Redeem exchanges a nonce for a session token: the nonce must be live and
// unused, and the member it names must be in the group right now.
//
// Every rejection returns ErrDenied with the reason logged, not returned.
func (a *Auth) Redeem(ctx context.Context, nonce string) (string, error) {
	if nonce == "" {
		return "", ErrDenied
	}
	key := noncePrefix + nonce

	// Consume first. This is the single-use gate, and it must happen before any
	// other check: bailing out early on an expired nonce would leave the record
	// behind, and a Get-then-Delete would let two concurrent redemptions of one
	// link both succeed.
	var (
		raw string
		ok  bool
		err error
	)
	if a.cfg.singleUse() {
		raw, ok, err = a.kv.Consume(ctx, key)
	} else {
		raw, ok, err = a.kv.Get(ctx, key)
	}
	if err != nil {
		slog.Error("webauth: nonce lookup failed", slog.String("err", err.Error()))
		return "", ErrDenied
	}
	if !ok {
		slog.Info("webauth: redeem denied — unknown or already-redeemed nonce")
		return "", ErrDenied
	}

	var rec nonceRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		slog.Error("webauth: corrupt nonce record", slog.String("err", err.Error()))
		return "", ErrDenied
	}
	now := a.clock()
	if now.Unix() > rec.Expires {
		slog.Info("webauth: redeem denied — link expired")
		return "", ErrDenied
	}

	group, err := types.ParseJID(rec.Group)
	if err != nil {
		slog.Error("webauth: unparseable group in nonce record", slog.String("err", err.Error()))
		return "", ErrDenied
	}
	member, err := types.ParseJID(rec.Member)
	if err != nil {
		slog.Error("webauth: unparseable member in nonce record", slog.String("err", err.Error()))
		return "", ErrDenied
	}

	if err := a.assertMember(ctx, group, member, "redeem"); err != nil {
		return "", err
	}
	return a.mint(group, member, now, now.Add(a.cfg.SessionTTL))
}

// Refresh re-checks membership and re-mints an expiring session token. The
// presented token must carry a valid signature and still be inside its absolute
// ceiling; its own expiry is allowed to have passed, since that is the very
// condition Refresh exists to resolve.
func (a *Auth) Refresh(ctx context.Context, token string) (string, error) {
	claims, err := a.parse(token)
	if err != nil {
		slog.Info("webauth: refresh denied — bad token", slog.String("err", err.Error()))
		return "", ErrDenied
	}
	now := a.clock()
	if now.Unix() >= claims.Absolute {
		slog.Info("webauth: refresh denied — past the absolute session ceiling")
		return "", ErrDenied
	}

	group, err := types.ParseJID(claims.Group)
	if err != nil {
		return "", ErrDenied
	}
	member, err := types.ParseJID(claims.Member)
	if err != nil {
		return "", ErrDenied
	}

	if err := a.assertMember(ctx, group, member, "refresh"); err != nil {
		return "", err
	}
	// The ceiling rides through unchanged — that is what stops refresh from
	// being an unbounded renewal.
	return a.mint(group, member, now, time.Unix(claims.Absolute, 0))
}

// Verify checks a token's signature and expiry without a membership query. The
// dashboard normally verifies locally with the shared key; this exists for
// callers that would rather ask the bot.
func (a *Auth) Verify(token string) (Claims, error) {
	claims, err := a.parse(token)
	if err != nil {
		return Claims{}, ErrDenied
	}
	now := a.clock().Unix()
	if now >= claims.Expires || now >= claims.Absolute {
		return Claims{}, ErrDenied
	}
	return claims, nil
}

// TTL is the lifetime of a freshly minted session token, for the expires_in
// field the endpoints report.
func (a *Auth) TTL() time.Duration { return a.cfg.RecheckInterval }

// assertMember runs the live membership check. A failed query denies: an
// unreachable WhatsApp must not become an open door.
func (a *Auth) assertMember(ctx context.Context, group, member types.JID, stage string) error {
	in, err := a.mem.IsMember(ctx, group, member)
	if err != nil {
		slog.Error("webauth: membership check failed — denying",
			slog.String("stage", stage), slog.String("err", err.Error()))
		return ErrDenied
	}
	if !in {
		slog.Info("webauth: denied — not a current group member", slog.String("stage", stage))
		return ErrDenied
	}
	return nil
}

// mint signs a session token valid for RecheckInterval, carrying absolute as
// the unmoving ceiling on the whole login.
func (a *Auth) mint(group, member types.JID, now, absolute time.Time) (string, error) {
	return a.sign(Claims{
		Group:    group.String(),
		Member:   member.String(),
		IssuedAt: now.Unix(),
		Expires:  now.Add(a.cfg.RecheckInterval).Unix(),
		Absolute: absolute.Unix(),
	})
}
