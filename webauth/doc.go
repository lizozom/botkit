// Package webauth implements membership-gated magic-link auth for a companion
// dashboard: MintLink (called from OnMessage, replied in-group), a single-use
// nonce store, Redeem (live IsMember check -> signed session token), and the
// /webauth/redeem ops endpoint. See ../SPEC.md section 9.
//
// Defaults: single-use link (15m TTL) + configurable long session (48h) +
// hourly membership recheck. Not yet implemented. Lands in Phase 3.
package webauth
