package transport

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func phone(u string) types.JID { return types.JID{User: u, Server: types.DefaultUserServer} }
func lid(u string) types.JID   { return types.JID{User: u, Server: types.HiddenUserServer} }

// TestSelfIsAdmin_RecognizesSelfPresentAsLID mirrors the amit/whatsapp-mgr
// daily-audit incident (2026-07-02): the bot can appear as a group participant
// under its hidden LID identity instead of its phone number. selfIsAdmin must
// still recognize that participant as the bot — otherwise the bot silently
// loses admin status (and therefore gating) for that group.
func TestSelfIsAdmin_RecognizesSelfPresentAsLID(t *testing.T) {
	self := phone("972000000000")
	selfLID := lid("119988776655")
	info := &types.GroupInfo{
		Participants: []types.GroupParticipant{
			{JID: selfLID, LID: selfLID, IsSuperAdmin: true}, // bot, addressed only by its LID
		},
	}
	if !selfIsAdmin(info, self, selfLID) {
		t.Fatal("selfIsAdmin must recognize the bot when listed under its LID identity")
	}
}

func TestParticipantIsSelf_NoIdentityNeverMatches(t *testing.T) {
	// With no known self identity, nothing should match (fail-safe).
	p := types.GroupParticipant{JID: phone("972111111111")}
	if participantIsSelf(p, types.JID{}, types.JID{}) {
		t.Error("participantIsSelf must not match when self identity is unknown")
	}
}

func TestParticipantIsSelf_MatchesPhoneForm(t *testing.T) {
	self := phone("972222222222")
	p := types.GroupParticipant{JID: phone("972222222222")}
	if !participantIsSelf(p, self, types.JID{}) {
		t.Error("participantIsSelf must match the bot's phone-form JID")
	}
}
