// Package send is the outbound path. v1 ships tier-1 only: reactive Reply,
// bound to an inbound message so it cannot initiate an unsolicited send. There
// is deliberately no raw Send(anyGroup, text). See ../SPEC.md section 7.
//
// Also home to the ErrPeerUnreachable / ErrBotWide send-error taxonomy lifted
// from whatsapp-nagger. Lands in Phase 2.
package send
