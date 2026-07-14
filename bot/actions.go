package bot

import (
	"context"

	"github.com/lizozom/botkit/transport"
	"go.mau.fi/whatsmeow/types"
)

// JoinRequest is a person awaiting approval to join a group. Phone is "" when
// the identity is an unresolvable LID.
type JoinRequest = transport.PendingRequest

// Member is a current group participant.
type Member = transport.Member

// Group is a joined group's metadata: name, admin status, member count, and
// community linkage.
type Group = transport.GroupSummary

// These are the guarded WhatsApp action methods (SPEC §6.3). Handlers —
// typically OnSchedule jobs — call them to do group work. Each WhatsApp write
// carries its anti-abuse hygiene inside. They are valid once the bot is
// connected (i.e. from within a running handler); calling before Run panics on
// a nil transport by design.

// ManagedGroups returns the configured managed group JIDs. Empty in AllGroups
// mode (there is no finite list to enumerate).
func (b *Bot) ManagedGroups() []types.JID { return b.groups.List() }

// AllGroups returns every group the account has joined — not just the managed
// ones — with name, admin status, member count, and community linkage. Use it
// for onboarding (picking JIDs for ManagedGroups) or diagnostics; gating
// decisions should still go through ManagedGroups/Allows, not this.
func (b *Bot) AllGroups(ctx context.Context) ([]Group, error) {
	return b.tp.AllGroups(ctx)
}

// PendingJoinRequests lists people awaiting approval for a group, with phones
// resolved. Guarded by 429 backoff.
func (b *Bot) PendingJoinRequests(ctx context.Context, group types.JID) ([]JoinRequest, error) {
	return b.tp.PendingRequests(ctx, group)
}

// ApproveJoin admits requesters to a group. Carries a short humanized pause so
// a batch of admits doesn't fire in one machine-instant burst.
func (b *Bot) ApproveJoin(ctx context.Context, group types.JID, requesters []types.JID) error {
	return b.tp.Approve(ctx, group, requesters)
}

// GroupMembers returns a group's current participants with resolved phones.
func (b *Bot) GroupMembers(ctx context.Context, group types.JID) ([]Member, error) {
	return b.tp.Members(ctx, group)
}

// ResolvePhone turns a WhatsApp identity into its phone (E.164 digits, no "+").
// Returns "" for an unresolvable LID.
func (b *Bot) ResolvePhone(ctx context.Context, jid types.JID) string {
	return b.tp.ResolvePhone(ctx, jid)
}

// SelfJID / SelfLID are the bot's own identity forms — use them to exclude the
// bot itself from a member audit (it can appear under either form).
func (b *Bot) SelfJID() types.JID { return b.tp.SelfJID() }
func (b *Bot) SelfLID() types.JID { return b.tp.SelfLID() }
