// Package webauth implements membership-gated magic-link auth for a companion
// dashboard: MintLink, Redeem, Refresh, and the /webauth/* endpoints.
//
// Three properties carry the security model: membership is checked live at
// redeem and every refresh, never trusted from the link; the nonce is consumed
// atomically, so a link redeems exactly once; and every failure looks identical
// to the caller, so the endpoints are not a membership oracle.
//
// See ../SPEC.md §9 for the design, ../docs/webauth.md for setup.
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

// ErrDenied is the only error any failure path returns — expired, replayed,
// forged, not-a-member, all identical. A distinguishable error would let a
// prober learn who is in a group. Real reasons go to the logs.
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

// New validates cfg and returns an Auth, failing fast on a missing secret
// rather than at someone's first login.
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

// MintLink returns a magic-link URL to reply with in-group.
//
// It does not check that member is in group. The check that counts runs live at
// redeem; one here would cost a WhatsApp query per mint and prove nothing about
// the state at redeem time.
func (a *Auth) MintLink(ctx context.Context, group, member types.JID) (string, error) {
	if group.IsEmpty() || member.IsEmpty() || member.User == "" {
		return "", errors.New("webauth: MintLink needs both a group and a member JID")
	}

	// GC on mint: links are minted far more often than redeemed, so unredeemed
	// ones would accumulate forever. Past LinkTTL means unusable anyway.
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

	// Consume first — this is the single-use gate. Checking expiry before
	// consuming would leave the record behind; a Get-then-Delete would let two
	// concurrent taps of one link both succeed.
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
// token must be validly signed and inside its absolute ceiling; its own expiry
// may have passed, which is the condition Refresh exists to resolve.
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
	// The ceiling rides through unchanged, so refresh is not unbounded renewal.
	return a.mint(group, member, now, time.Unix(claims.Absolute, 0))
}

// Verify checks signature and expiry without a membership query. The dashboard
// normally verifies locally; this is for callers that would rather ask the bot.
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

// TTL is a fresh token's lifetime, reported as expires_in.
func (a *Auth) TTL() time.Duration { return a.cfg.RecheckInterval }

// assertMember runs the live check. A failed query denies — an unreachable
// WhatsApp must not become an open door.
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

// mint signs a token valid for RecheckInterval, carrying absolute as the
// unmoving ceiling on the whole login.
func (a *Auth) mint(group, member types.JID, now, absolute time.Time) (string, error) {
	return a.sign(Claims{
		Group:    group.String(),
		Member:   member.String(),
		IssuedAt: now.Unix(),
		Expires:  now.Add(a.cfg.RecheckInterval).Unix(),
		Absolute: absolute.Unix(),
	})
}
