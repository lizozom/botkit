package bot

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

	// OpsAddr is the listen address for the private pairing ops API
	// (/pair, /status, /groups). Empty disables the ops API. Keep it private —
	// never expose this port publicly.
	OpsAddr string
	// OpsToken is the bearer token guarding the ops API. Empty = API refuses all.
	OpsToken string

	// AcceptMedia, when true, populates InboundMessage.Media (with a Download)
	// for image/video/audio/document/sticker messages. Default false: text bots
	// ignore media.
	AcceptMedia bool

	// ServiceName / ServiceVersion label telemetry. Optional.
	ServiceName    string
	ServiceVersion string
}
