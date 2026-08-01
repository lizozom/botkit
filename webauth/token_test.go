package webauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A token signed with a different key must never verify. This is the whole
// basis for the dashboard trusting a cookie without asking the bot.
func TestForeignKeyTokenRejected(t *testing.T) {
	ctx := context.Background()
	attackerCfg := testConfig()
	attackerCfg.SigningKey = "attacker-key"

	victim, _ := newAuth(t, testConfig(), newMembership(member))
	attacker, _ := newAuth(t, attackerCfg, newMembership(member))

	forged, err := attacker.Redeem(ctx, mustMint(t, attacker, group, member))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := victim.Verify(forged); err == nil {
		t.Error("a token signed with another key verified")
	}
}

// Re-signing tampered claims is the obvious attack: swap the member for someone
// else's and keep the old signature.
func TestTamperedClaimsRejected(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	token, err := a.Redeem(ctx, mustMint(t, a, group, member))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")

	var c Claims
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		mutar func(*Claims)
	}{
		{"different member", func(c *Claims) { c.Member = other.String() }},
		{"different group", func(c *Claims) { c.Group = "99999@g.us" }},
		{"extended expiry", func(c *Claims) { c.Expires += 86400 }},
		{"raised ceiling", func(c *Claims) { c.Absolute += 86400 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := c
			tc.mutar(&mutated)
			payload, err := json.Marshal(mutated)
			if err != nil {
				t.Fatal(err)
			}
			forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + parts[2]
			if _, err := a.parse(forged); err == nil {
				t.Error("tampered claims passed signature verification")
			}
		})
	}
}

// "alg": "none" and friends: the header is fixed, never consulted for how to
// verify, so a token claiming an unsigned algorithm is just a malformed token.
func TestAlgNoneRejected(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(Claims{
		Group: group.String(), Member: member.String(),
		IssuedAt: time.Now().Unix(), Expires: time.Now().Add(time.Hour).Unix(),
		Absolute: time.Now().Add(48 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)

	for _, forged := range []string{body + ".", body + ".AAAA", body} {
		if _, err := a.parse(forged); err == nil {
			t.Errorf("parse accepted an alg=none token: %q", forged)
		}
	}
}

func TestMalformedTokensRejected(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	for _, tok := range []string{
		"", ".", "..", "a.b", "a.b.c.d",
		jwtHeader + ".!!!.AAAA",
		jwtHeader + "." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".AAAA",
	} {
		if _, err := a.parse(tok); err == nil {
			t.Errorf("parse accepted malformed token %q", tok)
		}
	}
}

// Claims with a valid signature but no group/member must not pass — an empty
// group would otherwise authorize against whatever the caller supplies.
func TestEmptyClaimsRejected(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	signed, err := a.sign(Claims{IssuedAt: 1, Expires: 2, Absolute: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.parse(signed); err == nil {
		t.Error("parse accepted correctly-signed but empty claims")
	}
}

func TestRoundTrip(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	want := Claims{Group: group.String(), Member: member.String(), IssuedAt: 100, Expires: 200, Absolute: 300}
	token, err := a.sign(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}
