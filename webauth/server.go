package webauth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// Handler returns the /webauth/* routes, mounted by the bot on the private ops
// port next to the pairing API.
//
// Security model, in layers, mirroring package pairing:
//   - The port is never exposed publicly. The dashboard reaches it over
//     localhost, so these endpoints have no route from the internet and a
//     nonce cannot be guessed at from outside.
//   - Every request needs `Authorization: Bearer <APIToken>` (constant-time
//     compare).
//   - Every rejection is an identical opaque 403 — see ErrDenied.
func (a *Auth) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webauth/redeem", a.auth(a.handleRedeem))
	mux.HandleFunc("/webauth/refresh", a.auth(a.handleRefresh))
	return mux
}

func (a *Auth) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		want := "Bearer " + a.cfg.APIToken
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleRedeem exchanges {"nonce": ...} for a session token.
func (a *Auth) handleRedeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nonce string `json:"nonce"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := a.Redeem(r.Context(), body.Nonce)
	if err != nil {
		denied(w)
		return
	}
	writeSession(w, token, int(a.TTL().Seconds()))
}

// handleRefresh re-checks membership and re-mints {"session": ...}.
func (a *Auth) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session string `json:"session"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := a.Refresh(r.Context(), body.Session)
	if err != nil {
		denied(w)
		return
	}
	writeSession(w, token, int(a.TTL().Seconds()))
}

// decode enforces POST and reads a small JSON body. A malformed body is denied
// rather than described, so probing with junk yields the same 403 as everything
// else.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return false
	}
	// A magic-link nonce and a session token are both small; anything larger is
	// someone else's problem, not a login.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(v); err != nil {
		denied(w)
		return false
	}
	return true
}

func denied(w http.ResponseWriter) {
	http.Error(w, "denied", http.StatusForbidden)
}

func writeSession(w http.ResponseWriter, token string, expiresIn int) {
	w.Header().Set("Content-Type", "application/json")
	// A session token is a credential: keep it out of every cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session":    token,
		"expires_in": expiresIn,
	})
}
