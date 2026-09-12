package bot

import "github.com/lizozom/botkit/webauth"

// Config holds only framework-owned settings. Each app keeps its own domain
// config (LLM keys, roster paths, …) in its own struct. See ../SPEC.md §5.
type Config struct {
	// SessionDBPath is where the durable WhatsApp session lives. Survives
	// restarts; delete to force re-pair.
	SessionDBPath string

	// BotPhone is the number the bot links onto, used only for manual pairing.
	BotPhone string

	// ManagedGroups is the fail-closed JID whitelist. Events from any group not
	// listed here are dropped before handlers run. Empty = no groups.
	ManagedGroups []string

	// AllGroups, when true, makes the bot act in EVERY group the number belongs
	// to — a deliberate, fail-OPEN escape from the ManagedGroups whitelist.
	// Use only for bots that must reach everywhere (e.g. a transitional
	// announcement/apology bot). ManagedGroups is ignored when this is set.
	AllGroups bool

	// OpsAddr is the listen address for the private pairing ops API
	// (/pair, /status, /groups). Empty disables the ops API. Keep it private —
	// never expose this port publicly.
	OpsAddr string
	// OpsToken is the bearer token guarding the ops API. Empty = API refuses all.
	OpsToken string

	// WebAuth, when non-nil, enables membership-gated dashboard login and
	// mounts /webauth/redeem and /webauth/refresh on OpsAddr alongside the
	// pairing routes. Reach it from handlers via Bot.WebAuth(). Nil disables
	// the feature entirely. See ../docs/webauth.md.
	WebAuth *webauth.Config

	// RelayTarget is the single destination for relayed media (SPEC §7,
	// tier 1.5): a group, a contact, or the bot's own chat for a private feed.
	// Empty disables relaying entirely — InboundMessage.Relay then returns
	// ErrRelayDisabled.
	//
	// It is one fixed JID on purpose. Handlers choose WHETHER to relay an
	// inbound message, never WHERE it goes, so the tier cannot become a
	// broadcast primitive.
	RelayTarget string

	// RelayDailyCap bounds relayed sends per UTC day. Zero means
	// DefaultRelayDailyCap; negative disables the cap (don't).
	RelayDailyCap int

	// AcceptMedia, when true, populates InboundMessage.Media (with a Download)
	// for image/video/audio/document/sticker messages. Default false: text bots
	// ignore media.
	AcceptMedia bool

	// Telemetry is not configured here. The app calls telemetry.Init itself
	// with its own service name and version — see SPEC §13.
}
