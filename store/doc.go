// Package store owns only cross-cutting persistence: the WhatsApp session-DB
// open (WAL/busy_timeout DSN), a generic metadata key-value table (scheduler
// idempotency, webauth nonces, last-fired dates), and scheduler-run
// bookkeeping. Apps own all domain tables; SQLite stays their source of truth.
// See ../SPEC.md section 11.
//
// Not yet implemented. Session-open lands in Phase 1 (with the transport);
// metadata KV + run bookkeeping in Phase 3 (with the scheduler).
package store
