// Package gate enforces group access: a fail-closed JID whitelist that drops
// events from non-managed groups before any handler runs. See ../SPEC.md
// section 8.
//
// DM access is gated separately (by the DM handler's policy in package bot),
// because the group whitelist can't apply to a 1:1 chat.
package gate

import (
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// Groups is a fail-closed whitelist of managed group JIDs. A nil or empty
// Groups allows nothing — the bot acts in zero groups unless explicitly told.
//
// The one exception is AllowAll: a group set built by AllowAll() allows every
// group. That is a deliberate, opt-in escape from fail-closed — use it only for
// bots that genuinely must act everywhere (e.g. a transitional announcement/
// apology bot), knowing it responds in every group the number belongs to.
type Groups struct {
	allowed map[string]bool
	list    []types.JID
	all     bool
}

// AllowAll returns a Groups that allows every group. Fail-OPEN — see the type
// doc. Callers opt in explicitly.
func AllowAll() *Groups {
	return &Groups{all: true}
}

// ParseGroups builds a whitelist from JID strings (blanks skipped, dupes
// collapsed). It errors on a malformed JID so a typo fails loudly at boot
// rather than silently gating nothing. An empty input is valid (allows
// nothing) — a DM-only bot legitimately has no managed groups.
func ParseGroups(jids []string) (*Groups, error) {
	g := &Groups{allowed: make(map[string]bool)}
	for _, s := range jids {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		jid, err := types.ParseJID(s)
		if err != nil {
			return nil, fmt.Errorf("bad group JID %q: %w", s, err)
		}
		// ParseJID is lenient (a bare string defaults to a user server), so
		// require the group server explicitly — this catches typos and someone
		// pasting a user JID where a group JID belongs.
		if jid.Server != types.GroupServer {
			return nil, fmt.Errorf("not a group JID %q (want <id>@g.us)", s)
		}
		key := jid.String()
		if !g.allowed[key] {
			g.allowed[key] = true
			g.list = append(g.list, jid)
		}
	}
	return g, nil
}

// Allows reports whether jid is a managed group. Fail-closed: false for a nil
// receiver or an empty whitelist. Always true when built by AllowAll.
func (g *Groups) Allows(jid types.JID) bool {
	if g == nil {
		return false
	}
	return g.all || g.allowed[jid.String()]
}

// AllowsAll reports whether this set is in fail-open (all-groups) mode.
func (g *Groups) AllowsAll() bool { return g != nil && g.all }

// List returns the managed group JIDs.
func (g *Groups) List() []types.JID {
	if g == nil {
		return nil
	}
	return g.list
}

// Empty reports whether the whitelist gates no groups at all. An AllowAll set
// is never empty.
func (g *Groups) Empty() bool {
	if g == nil {
		return true
	}
	return !g.all && len(g.allowed) == 0
}
