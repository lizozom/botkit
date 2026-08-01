package webauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Claims is a session token's payload. Sessions are group-scoped: a token
// proves "a current member of Group", so a bot managing several groups must
// key dashboard data on Group rather than assume one session sees everything.
type Claims struct {
	Group    string `json:"grp"`
	Member   string `json:"mbr"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"` // IssuedAt + RecheckInterval
	Absolute int64  `json:"abs"` // first login + SessionTTL; never extended
}

// jwtHeader is the fixed HS256 header. Tokens are ordinary JWTs so the
// dashboard can verify them with any off-the-shelf library.
const jwtHeader = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}

func (a *Auth) sign(c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("webauth: encode claims: %w", err)
	}
	body := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(a.mac(body)), nil
}

// parse verifies a token's signature and decodes its claims. It does not check
// expiry — callers apply the time rules they need, since Refresh must accept a
// token whose exp has already passed.
//
// The algorithm is never read from the header. A token is valid only if it is
// byte-identical to what this key would have produced, which is what makes the
// "alg": "none" and algorithm-confusion families structurally impossible here.
func (a *Auth) parse(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("malformed token")
	}
	if parts[0] != jwtHeader {
		return Claims{}, errors.New("unexpected token header")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, errors.New("malformed signature")
	}
	if !hmac.Equal(sig, a.mac(parts[0]+"."+parts[1])) {
		return Claims{}, errors.New("bad signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("malformed payload")
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, errors.New("malformed claims")
	}
	if c.Group == "" || c.Member == "" {
		return Claims{}, errors.New("incomplete claims")
	}
	return c, nil
}

func (a *Auth) mac(body string) []byte {
	h := hmac.New(sha256.New, []byte(a.cfg.SigningKey))
	h.Write([]byte(body))
	return h.Sum(nil)
}
