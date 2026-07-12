package gate

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func group(u string) types.JID { return types.JID{User: u, Server: types.GroupServer} }

func TestParseAndAllows(t *testing.T) {
	g, err := ParseGroups([]string{"120363@g.us", " ", "120363@g.us"})
	if err != nil {
		t.Fatalf("ParseGroups: %v", err)
	}
	if len(g.List()) != 1 {
		t.Errorf("dupes/blanks not collapsed: %v", g.List())
	}
	if !g.Allows(group("120363")) {
		t.Error("managed group should be allowed")
	}
	if g.Allows(group("999999")) {
		t.Error("unmanaged group must be denied")
	}
}

func TestFailClosed(t *testing.T) {
	var g *Groups // nil
	if g.Allows(group("120363")) {
		t.Error("nil Groups must allow nothing")
	}
	empty, _ := ParseGroups(nil)
	if !empty.Empty() {
		t.Error("no JIDs should be Empty")
	}
	if empty.Allows(group("120363")) {
		t.Error("empty whitelist must allow nothing (fail-closed)")
	}
}

func TestAllowAll(t *testing.T) {
	g := AllowAll()
	if !g.Allows(group("120363")) || !g.Allows(group("999999")) {
		t.Error("AllowAll must allow any group")
	}
	if !g.AllowsAll() {
		t.Error("AllowsAll should report true")
	}
	if g.Empty() {
		t.Error("AllowAll must not be Empty")
	}
}

func TestBadJID(t *testing.T) {
	if _, err := ParseGroups([]string{"not a jid"}); err == nil {
		t.Error("malformed JID must error")
	}
}
