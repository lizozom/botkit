// Package gate enforces group access: a fail-closed JID whitelist
// (ManagedGroups) that drops events from non-managed groups before any handler
// runs, plus an optional secondary phone allowlist. See ../SPEC.md section 8.
//
// Not yet implemented. Lands in Phase 2.
package gate
