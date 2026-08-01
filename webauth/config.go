package webauth

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Config holds the webauth knobs. The three secrets are required; the four
// timing fields default to the values documented in ../docs/webauth.md, so a
// Config carrying only secrets behaves exactly as that page describes.
type Config struct {
	// DashboardURL is the public base URL magic links are built from, e.g.
	// "https://dash.example.com". Required.
	DashboardURL string

	// APIToken is the bearer token the dashboard presents to /webauth/redeem
	// and /webauth/refresh. Required.
	APIToken string

	// SigningKey is the HMAC key session tokens are signed with, shared with
	// the dashboard so it can verify cookies locally. Required, and distinct
	// from APIToken by design: one leaked value must not grant both "call the
	// endpoints" and "forge any session".
	SigningKey string

	// LinkTTL is how long a magic link stays redeemable. Default 15m. Short by
	// intent — a screenshot of the group chat goes stale fast.
	LinkTTL time.Duration

	// LinkSingleUse consumes the link on first redeem. Default true. Setting it
	// false yields a literally-reusable link (until LinkTTL) and gives up the
	// "already-redeemed link is dead" property; do it deliberately or not at all.
	LinkSingleUse *bool

	// SessionTTL is the absolute ceiling on one login: past this the user
	// returns to the group for a fresh link, regardless of refreshes. Default 48h.
	SessionTTL time.Duration

	// RecheckInterval is the session token's lifetime, and therefore the
	// revocation lag: someone removed from the group keeps access for at most
	// this long. Each refresh costs one live group query per active user.
	// Default 1h.
	RecheckInterval time.Duration
}

// Default timings. See ../SPEC.md §9 for why these values.
const (
	DefaultLinkTTL         = 15 * time.Minute
	DefaultSessionTTL      = 48 * time.Hour
	DefaultRecheckInterval = time.Hour
)

// Validate reports whether the config is usable, so a bot can reject a missing
// or duplicated secret at construction rather than at someone's first login.
func (c Config) Validate() error {
	_, err := c.withDefaults()
	return err
}

// withDefaults fills unset timing fields and validates the required ones.
func (c Config) withDefaults() (Config, error) {
	if strings.TrimSpace(c.DashboardURL) == "" {
		return c, errors.New("webauth: DashboardURL is required")
	}
	u, err := url.Parse(c.DashboardURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return c, errors.New("webauth: DashboardURL must be an absolute URL, e.g. https://dash.example.com")
	}
	if c.APIToken == "" {
		return c, errors.New("webauth: APIToken is required (the endpoints refuse every request without one)")
	}
	if c.SigningKey == "" {
		return c, errors.New("webauth: SigningKey is required")
	}
	if c.APIToken == c.SigningKey {
		return c, errors.New("webauth: APIToken and SigningKey must differ — sharing one value means a leaked API token also forges sessions")
	}
	c.DashboardURL = strings.TrimSuffix(c.DashboardURL, "/")

	if c.LinkTTL <= 0 {
		c.LinkTTL = DefaultLinkTTL
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = DefaultSessionTTL
	}
	if c.RecheckInterval <= 0 {
		c.RecheckInterval = DefaultRecheckInterval
	}
	if c.LinkSingleUse == nil {
		single := true
		c.LinkSingleUse = &single
	}
	if c.RecheckInterval > c.SessionTTL {
		return c, errors.New("webauth: RecheckInterval exceeds SessionTTL — membership would never be re-checked within a session")
	}
	return c, nil
}

func (c Config) singleUse() bool { return c.LinkSingleUse == nil || *c.LinkSingleUse }
