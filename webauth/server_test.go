package webauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, h http.Handler, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRedeemEndpoint(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	h := a.Handler()
	nonce := mustMint(t, a, group, member)

	rec := post(t, h, "/webauth/redeem", "api-token-value", `{"nonce":"`+nonce+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var out struct {
		Session   string `json:"session"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Verify(out.Session); err != nil {
		t.Errorf("returned session does not verify: %v", err)
	}
	if out.ExpiresIn != int(DefaultRecheckInterval.Seconds()) {
		t.Errorf("expires_in = %d, want %d", out.ExpiresIn, int(DefaultRecheckInterval.Seconds()))
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — a session token must not be cached", got)
	}
	_ = ctx
}

func TestEndpointsRequireBearerToken(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	h := a.Handler()
	nonce := mustMint(t, a, group, member)
	body := `{"nonce":"` + nonce + `"}`

	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"wrong token", "not-the-token"},
		{"the signing key", "signing-key-value"}, // must not double as API access
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/webauth/redeem", "/webauth/refresh"} {
				if rec := post(t, h, path, tc.token, body); rec.Code != http.StatusUnauthorized {
					t.Errorf("%s status = %d, want 401", path, rec.Code)
				}
			}
		})
	}

	// The rejected requests must not have consumed the nonce.
	if rec := post(t, h, "/webauth/redeem", "api-token-value", body); rec.Code != http.StatusOK {
		t.Errorf("nonce was consumed by unauthorized attempts: status %d", rec.Code)
	}
}

// Every failure mode looks the same from outside. Anything else is a
// membership oracle: "which of these people is in that group?"
func TestFailuresAreIndistinguishable(t *testing.T) {
	ctx := context.Background()
	mem := newMembership(member)
	a, _ := newAuth(t, testConfig(), mem)
	h := a.Handler()

	used := mustMint(t, a, group, member)
	if _, err := a.Redeem(ctx, used); err != nil {
		t.Fatal(err)
	}
	exMember := mustMint(t, a, group, other) // minted for a non-member

	bodies := map[string]string{
		"unknown nonce":     `{"nonce":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
		"already redeemed":  `{"nonce":"` + used + `"}`,
		"not a member":      `{"nonce":"` + exMember + `"}`,
		"empty nonce":       `{"nonce":""}`,
		"malformed request": `not json at all`,
	}
	var seen []string
	for name, body := range bodies {
		rec := post(t, h, "/webauth/redeem", "api-token-value", body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
		seen = append(seen, rec.Body.String())
	}
	for _, got := range seen[1:] {
		if got != seen[0] {
			t.Errorf("responses differ between failure modes: %q vs %q", seen[0], got)
		}
	}
}

func TestRefreshEndpoint(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	h := a.Handler()

	token, err := a.Redeem(ctx, mustMint(t, a, group, member))
	if err != nil {
		t.Fatal(err)
	}
	rec := post(t, h, "/webauth/refresh", "api-token-value", `{"session":"`+token+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	// A garbage token is denied, not described.
	if rec := post(t, h, "/webauth/refresh", "api-token-value", `{"session":"nope"}`); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestGetIsRejected(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	h := a.Handler()
	for _, path := range []string{"/webauth/redeem", "/webauth/refresh"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer api-token-value")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", path, rec.Code)
		}
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	a, _ := newAuth(t, testConfig(), newMembership(member))
	huge := `{"nonce":"` + strings.Repeat("A", 32<<10) + `"}`
	if rec := post(t, a.Handler(), "/webauth/redeem", "api-token-value", huge); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
