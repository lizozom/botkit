// Command devserver runs botkit's real webauth endpoints against a fake group,
// so the dashboard side of the flow can be built and tested without pairing a
// WhatsApp number.
//
// Everything security-critical is the real thing: the same Auth, the same
// nonce store, the same /webauth/redeem and /webauth/refresh handlers, the same
// token signing. Only the membership source is swapped — instead of asking
// WhatsApp who is in the group, it reads an in-memory set you control from the
// harness UI. That is the one substitution, and it is the point: it lets you
// remove someone from "the group" and watch the dashboard log them out.
//
// The extra /dev/* routes stand in for a WhatsApp group: /dev/mint is someone
// typing "dashboard", /dev/members edits the membership. They exist only here,
// never in botkit itself.
//
// NOT FOR PRODUCTION. It ships fixed dev secrets and an unauthenticated
// control API that mints a session for anyone who asks. Localhost only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/lizozom/botkit/store"
	"github.com/lizozom/botkit/webauth"
	"go.mau.fi/whatsmeow/types"
)

// Fixed dev values so the Next.js app can hardcode them in .env.local. Real
// deployments generate these; see ../../docs/webauth.md.
const (
	devAPIToken   = "dev-api-token-not-a-secret"
	devSigningKey = "dev-signing-key-not-a-secret"
)

// devGroup stands in for a managed WhatsApp group.
var devGroup = types.JID{User: "120363000000000000", Server: types.GroupServer}

// fakeGroup is the membership source: an in-memory set instead of a live
// WhatsApp query. Satisfies webauth.Membership.
type fakeGroup struct {
	mu      sync.RWMutex
	members map[string]string // JID string -> display name
}

func (f *fakeGroup) IsMember(_ context.Context, _ types.JID, member types.JID) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.members[member.String()]
	return ok, nil
}

func (f *fakeGroup) list() []map[string]string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]map[string]string, 0, len(f.members))
	for jid, name := range f.members {
		out = append(out, map[string]string{"jid": jid, "name": name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["jid"] < out[j]["jid"] })
	return out
}

func (f *fakeGroup) add(jid, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[jid] = name
}

func (f *fakeGroup) remove(jid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.members, jid)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (keep it on localhost)")
	dashboard := flag.String("dashboard", "http://localhost:3000", "dashboard base URL for magic links")
	// Production defaults are minutes and hours, which makes the expiry paths
	// untestable by hand. Shrink them to seconds to watch a link go stale, or a
	// removed member get logged out, without waiting an hour.
	linkTTL := flag.Duration("link-ttl", webauth.DefaultLinkTTL, "how long a magic link stays redeemable")
	recheck := flag.Duration("recheck", webauth.DefaultRecheckInterval, "session token lifetime — how often membership is re-checked")
	sessionTTL := flag.Duration("session-ttl", webauth.DefaultSessionTTL, "absolute ceiling on one login")
	flag.Parse()

	// Two people to play with: one in the group, one never in it.
	group := &fakeGroup{members: map[string]string{
		"972546260906@s.whatsapp.net": "Liza (member)",
		"11223344@lid":                "LID-only member",
	}}

	dir, err := os.MkdirTemp("", "webauth-dev")
	if err != nil {
		fatal(err)
	}
	kv, err := store.NewKV(filepath.Join(dir, "state.db"))
	if err != nil {
		fatal(err)
	}
	defer kv.Close()

	auth, err := webauth.New(webauth.Config{
		DashboardURL:    *dashboard,
		APIToken:        devAPIToken,
		SigningKey:      devSigningKey,
		LinkTTL:         *linkTTL,
		RecheckInterval: *recheck,
		SessionTTL:      *sessionTTL,
	}, kv, group)
	if err != nil {
		fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/webauth/", auth.Handler()) // the real endpoints, unmodified

	// --- dev-only harness routes, standing in for WhatsApp ---

	// POST /dev/mint {"member": "<jid>"} — someone types "dashboard" in the group.
	mux.HandleFunc("/dev/mint", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Member string `json:"member"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		member, err := types.ParseJID(body.Member)
		if err != nil {
			http.Error(w, "bad member JID", http.StatusBadRequest)
			return
		}
		link, err := auth.MintLink(r.Context(), devGroup, member)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("dev: minted link", slog.String("member", body.Member))
		writeJSON(w, map[string]string{"link": link})
	})

	// GET /dev/members — who is in the group.
	// POST /dev/members {"jid","name","action":"add"|"remove"} — edit it.
	mux.HandleFunc("/dev/members", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, map[string]any{"group": devGroup.String(), "members": group.list()})
			return
		}
		var body struct {
			JID    string `json:"jid"`
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		switch body.Action {
		case "remove":
			group.remove(body.JID)
			slog.Info("dev: removed from group", slog.String("jid", body.JID))
		default:
			name := body.Name
			if name == "" {
				name = body.JID
			}
			group.add(body.JID, name)
			slog.Info("dev: added to group", slog.String("jid", body.JID))
		}
		writeJSON(w, map[string]any{"members": group.list()})
	})

	slog.Info("webauth devserver up — fake group, real auth",
		slog.String("addr", *addr), slog.String("dashboard", *dashboard),
		slog.Duration("link_ttl", *linkTTL), slog.Duration("recheck", *recheck),
		slog.Duration("session_ttl", *sessionTTL))
	if err := http.ListenAndServe(*addr, cors(mux)); err != nil {
		fatal(err)
	}
}

// cors lets the harness page call /dev/* straight from the browser. Dev-only:
// the production endpoints are reached server-side and need no CORS at all.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "content-type, authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fatal(err error) {
	slog.Error("devserver", slog.String("err", err.Error()))
	os.Exit(1)
}
