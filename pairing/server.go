// Package pairing is the tiny private HTTP surface for human-initiated
// WhatsApp pairing. POST /pair generates a pairing code ONLY when hit — the bot
// never pairs on its own (repeated automated pairing is a suspected Meta
// abuse-detection trigger). See ../SPEC.md section 12.
//
// Security model, in layers:
//   - The port is never exposed publicly; reach it over a private network
//     (e.g. `fly proxy 8080` + curl localhost).
//   - Every request needs `Authorization: Bearer <token>` (constant-time
//     compare). With no token configured the whole API refuses requests.
//   - /pair has a cooldown so even an authed script can't hammer WhatsApp.
//
// De-domained from amit/whatsapp-mgr's internal/pairapi: the app-specific
// /sweep endpoint is dropped (periodic work is an OnSchedule job, not an ops
// endpoint).
package pairing

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// pairCooldown is the minimum gap between pairing attempts — one impatient
// retry loop must not become a burst of pairing-code requests.
const pairCooldown = 2 * time.Minute

// Group is one joined group, as reported by GET /groups — used by operators to
// pick JIDs for the managed-groups whitelist.
type Group struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
	Members int    `json:"members"`
}

// WhatsApp is the slice of the transport the API needs (faked in tests). The
// phone to pair with is bound by the caller before Pair is exposed here.
type WhatsApp interface {
	Paired() bool
	Connected() bool
	Pair(ctx context.Context) (code string, err error)
	Groups(ctx context.Context) ([]Group, error)
}

// Server handles the pairing API. Zero value is not usable; use New.
type Server struct {
	token string
	wa    WhatsApp
	clock func() time.Time // injectable for tests

	mu       sync.Mutex
	lastPair time.Time
}

// New builds a Server. An empty token disables the API (all requests 403).
func New(token string, wa WhatsApp) *Server {
	return &Server{token: token, wa: wa, clock: time.Now}
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz) // unauthenticated liveness probe
	mux.HandleFunc("/pair", s.auth(s.handlePair))
	mux.HandleFunc("/status", s.auth(s.handleStatus))
	mux.HandleFunc("/groups", s.auth(s.handleGroups))
	return mux
}

// handleHealthz is an unauthenticated liveness probe for platform health checks
// (e.g. Fly). It returns 200 as long as the process serves HTTP — it does NOT
// gate on WhatsApp being connected, so an unpaired/reconnecting bot is not
// restart-looped. Connection detail rides in the body for humans; use the
// token-gated /status for a machine-readable readiness signal.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"paired":    s.wa.Paired(),
		"connected": s.wa.Connected(),
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			http.Error(w, "pairing API disabled: token is not configured", http.StatusForbidden)
			return
		}
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.token
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// GETs (browsers, link prefetchers) must never trigger pairing.
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if s.wa.Paired() {
		http.Error(w, "already paired — nothing to do", http.StatusConflict)
		return
	}

	s.mu.Lock()
	if since := s.clock().Sub(s.lastPair); !s.lastPair.IsZero() && since < pairCooldown {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("a pairing attempt ran %s ago — wait %s between attempts",
			since.Round(time.Second), pairCooldown), http.StatusTooManyRequests)
		return
	}
	s.lastPair = s.clock()
	s.mu.Unlock()

	code, err := s.wa.Pair(r.Context())
	if err != nil {
		http.Error(w, "pairing failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"pairing_code": code,
		"instructions": "On the phone that owns the bot's number: WhatsApp > Linked Devices > Link a device > Link with phone number instead, then type this code.",
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"paired":    s.wa.Paired(),
		"connected": s.wa.Connected(),
	})
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if !s.wa.Paired() {
		http.Error(w, "not paired yet", http.StatusConflict)
		return
	}
	groups, err := s.wa.Groups(r.Context())
	if err != nil {
		http.Error(w, "list groups: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
