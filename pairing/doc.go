// Package pairing holds the manual-only pair-code flow and the ops API
// endpoints (POST /pair with cooldown, GET /status, GET /groups). The bot
// never requests a pair code on its own — see ../SPEC.md section 12.
//
// Not yet implemented. Lands in Phase 1 (based on amit/whatsapp-mgr's
// internal/pairapi).
package pairing
