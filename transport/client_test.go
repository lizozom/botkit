package transport

import (
	"strings"
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

// Approving is the one call that actually lets someone into a group, so a
// refusal read as success strands them: callers record "admitted" and never
// reconsider.

func TestParticipantErrors_AllAccepted(t *testing.T) {
	results := []types.GroupParticipant{
		{JID: phone("972111111111")},
		{JID: phone("972222222222")}, // Error 0 = no code attached
	}
	if err := participantErrors(results); err != nil {
		t.Errorf("nobody was refused, want nil, got %v", err)
	}
}

func TestParticipantErrors_EmptyReply(t *testing.T) {
	if err := participantErrors(nil); err != nil {
		t.Errorf("nothing to report, want nil, got %v", err)
	}
}

func TestParticipantErrors_RefusalIsAnError(t *testing.T) {
	results := []types.GroupParticipant{{JID: phone("972333333333"), Error: 403}}
	err := participantErrors(results)
	if err == nil {
		t.Fatal("WhatsApp refused the participant (error 403) — that must never read as success")
	}
	if !strings.Contains(err.Error(), "972333333333") || !strings.Contains(err.Error(), "403") {
		t.Errorf("error should name who was refused and why, got: %v", err)
	}
}

func TestParticipantErrors_PartialRefusalNamesOnlyTheRefused(t *testing.T) {
	results := []types.GroupParticipant{
		{JID: phone("972444444444")},             // admitted
		{JID: phone("972555555555"), Error: 404}, // refused
	}
	err := participantErrors(results)
	if err == nil {
		t.Fatal("one participant in the batch was refused, want an error")
	}
	if strings.Contains(err.Error(), "972444444444") {
		t.Errorf("must not blame the participant who was admitted, got: %v", err)
	}
	if !strings.Contains(err.Error(), "972555555555") {
		t.Errorf("must name the refused participant, got: %v", err)
	}
}
