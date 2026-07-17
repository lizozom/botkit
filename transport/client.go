// Package transport is botkit's WhatsApp transport over whatsmeow: a durable
// session, connect / auto-reconnect, manual-only pairing, health, LID→phone
// resolution, listing/approving join requests, and reading group members.
//
// De-domained from amit/whatsapp-mgr's internal/wa. Two deliberate changes for
// reuse: the email notifier is replaced by an OnLoggedOut hook (the app decides
// how to alert), and phone canonicalization goes through botkit's identity
// package instead of a hard-wired resolver.
//
// See ../SPEC.md sections 3, 4, 12.
package transport

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lizozom/botkit/identity"
	"github.com/lizozom/botkit/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// ErrNotPaired is returned by Connect when there is no saved session. Pairing
// is a separate, always human-initiated step — see Pair.
var ErrNotPaired = fmt.Errorf("not paired — a human must trigger pairing (Pair)")

// Client is the bot's WhatsApp session.
type Client struct {
	wm          *whatsmeow.Client
	sessionDB   *sql.DB
	onConnected func()
	onLoggedOut func(reason string)
	onMessage   func(*events.Message)
}

// PendingRequest is a person asking to join a group. Phone is "" when the
// identity is an unresolvable LID (handled by the native-WhatsApp fallback).
type PendingRequest struct {
	JID   types.JID
	Phone string // E.164 User part (no '+'), "" if unresolved
	LID   string // the @lid JID string when the requester arrived as a LID
}

// Member is a current group participant.
type Member struct {
	JID     types.JID
	Phone   string
	IsAdmin bool
}

// GroupSummary is a group's JID, name, community relationship, and members —
// all from a single batched GetJoinedGroups call (no per-group queries).
type GroupSummary struct {
	JID         types.JID
	Name        string
	ParentJID   types.JID // the community this group belongs to (empty if standalone)
	IsCommunity bool      // true if this group IS a community parent
	IsAdmin     bool      // whether the bot is an admin here
	Members     []Member
}

// New opens the durable session DB (via store.OpenSQLite), builds the whatsmeow
// client, and registers the event handler. Call Connect to go online.
func New(ctx context.Context, dbPath string) (*Client, error) {
	sqlDB, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	container := sqlstore.NewWithDB(sqlDB, "sqlite3", nil)
	if err := container.Upgrade(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("upgrade session store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("get device: %w", err)
	}
	c := &Client{
		// waLog.Stdout surfaces whatsmeow's handshake/pairing chatter — without
		// it, pairing failures are invisible on the server side.
		wm:        whatsmeow.NewClient(device, waLog.Stdout("whatsmeow", "INFO", false)),
		sessionDB: sqlDB,
	}
	c.wm.AddEventHandler(c.handleEvent)
	return c, nil
}

// Paired reports whether the device has completed pairing.
func (c *Client) Paired() bool { return c.wm.Store.ID != nil }

// Connected reports whether the socket is up and the session logged in.
func (c *Client) Connected() bool { return c.wm.IsConnected() && c.wm.IsLoggedIn() }

// SetOnConnected registers a hook invoked every time the session comes online
// (first connect after pairing, and every reconnect). Wrap with sync.Once for
// run-once semantics.
func (c *Client) SetOnConnected(fn func()) { c.onConnected = fn }

// SetOnLoggedOut registers a hook invoked when the session is lost (device
// revoked / logged out). This is where an app alerts an operator. The bot must
// NOT re-pair automatically — that is a suspected Meta abuse-detection trigger.
func (c *Client) SetOnLoggedOut(fn func(reason string)) { c.onLoggedOut = fn }

// Connect goes online using the saved session. It never pairs: an unpaired
// client gets ErrNotPaired and the caller decides what a human should do.
func (c *Client) Connect(ctx context.Context) error {
	if !c.Paired() {
		return ErrNotPaired
	}
	return c.wm.Connect()
}

// Pair performs phone (link-code) pairing and returns the code to give the
// human. It must only ever run on explicit operator action — never from an
// event handler, scheduler, or restart path.
func (c *Client) Pair(ctx context.Context, pairPhone string) (string, error) {
	if c.Paired() {
		return "", fmt.Errorf("already paired")
	}
	if pairPhone == "" {
		return "", fmt.Errorf("cannot pair: bot phone not set")
	}

	// Phone pairing requires the websocket fully up before PairPhone's server
	// IQ (else "info query returned status 400"). Register the QR channel BEFORE
	// Connect (whatsmeow requirement) and use its first event purely as a
	// "socket ready" signal — we never display the QR.
	qrChan, err := c.wm.GetQRChannel(ctx)
	if err != nil {
		return "", fmt.Errorf("qr channel: %w", err)
	}
	if err := c.wm.Connect(); err != nil {
		return "", fmt.Errorf("connect for pairing: %w", err)
	}
	select {
	case <-qrChan:
		// connected and ready for pairing
	case <-time.After(30 * time.Second):
		return "", fmt.Errorf("timed out waiting for WhatsApp connection before pairing")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	go func() { // drain remaining QR refreshes so the emitter never blocks
		for range qrChan {
		}
	}()

	// identity.Normalize accepts local forms (e.g. 0546260906); PairPhone needs
	// full international digits.
	code, err := c.wm.PairPhone(ctx, identity.Normalize(pairPhone), true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", fmt.Errorf("pair phone: %w", err)
	}
	slog.Info("pairing code issued")
	return code, nil
}

// Disconnect cleanly closes the connection.
func (c *Client) Disconnect() { c.wm.Disconnect() }

// Ping is a liveness probe: session DB responsive + logged in + connected.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.sessionDB.PingContext(ctx); err != nil {
		return fmt.Errorf("session db: %w", err)
	}
	if !c.wm.IsLoggedIn() {
		return fmt.Errorf("not logged in (re-pair required)")
	}
	if !c.wm.IsConnected() {
		return fmt.Errorf("socket not connected")
	}
	return nil
}

// ResolvePhone turns a WhatsApp identity into its phone User part (3-tier:
// direct phone JID, then the local LID→PN store). Returns "" for an
// unresolvable LID.
func (c *Client) ResolvePhone(ctx context.Context, jid types.JID) string {
	if jid.Server == types.DefaultUserServer {
		return jid.User
	}
	if c.wm.Store != nil && c.wm.Store.LIDs != nil {
		pn, err := c.wm.Store.LIDs.GetPNForLID(ctx, jid)
		if err != nil {
			slog.Debug("LID→PN lookup failed", slog.String("error", err.Error()))
			return ""
		}
		if pn.Server == types.DefaultUserServer {
			return pn.User
		}
	}
	return ""
}

// SelfJID returns the bot's own phone-form JID (to exclude itself from audits).
func (c *Client) SelfJID() types.JID {
	if c.wm.Store.ID == nil {
		return types.JID{}
	}
	return *c.wm.Store.ID
}

// SelfLID returns the bot's own LID-form JID (its "hidden" identity). Group
// members carrying the bot's own identity sometimes appear in this form.
func (c *Client) SelfLID() types.JID { return c.wm.Store.LID }

// AllGroups returns every joined group (admin or not) with metadata, from ONE
// batched GetJoinedGroups call.
func (c *Client) AllGroups(ctx context.Context) ([]GroupSummary, error) {
	var groups []*types.GroupInfo
	err := c.withRateRetry(ctx, func() error {
		var e error
		groups, e = c.wm.GetJoinedGroups(ctx)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("list joined groups: %w", err)
	}
	self, selfLID := c.SelfJID(), c.SelfLID()
	out := make([]GroupSummary, 0, len(groups))
	for _, g := range groups {
		out = append(out, GroupSummary{
			JID:         g.JID,
			Name:        g.Name,
			ParentJID:   g.LinkedParentJID,
			IsCommunity: g.IsParent,
			IsAdmin:     selfIsAdmin(g, self, selfLID),
			Members:     c.membersFrom(ctx, g.Participants),
		})
	}
	return out, nil
}

// Members returns the current participants of a single group with resolved
// phones. Prefer AllGroups for multi-group/audit work (one batched call).
func (c *Client) Members(ctx context.Context, group types.JID) ([]Member, error) {
	var info *types.GroupInfo
	err := c.withRateRetry(ctx, func() error {
		var e error
		info, e = c.wm.GetGroupInfo(ctx, group)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("get group info: %w", err)
	}
	return c.membersFrom(ctx, info.Participants), nil
}

// PendingRequests lists people awaiting approval for a group, resolving phones.
// Join requests have no batch API, so this is per-group — guarded by 429 backoff.
func (c *Client) PendingRequests(ctx context.Context, group types.JID) ([]PendingRequest, error) {
	var reqs []types.GroupParticipantRequest
	err := c.withRateRetry(ctx, func() error {
		var e error
		reqs, e = c.wm.GetGroupRequestParticipants(ctx, group)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("get group requests: %w", err)
	}
	out := make([]PendingRequest, 0, len(reqs))
	for _, r := range reqs {
		pr := PendingRequest{JID: r.JID, Phone: c.ResolvePhone(ctx, r.JID)}
		if r.JID.Server == types.HiddenUserServer {
			pr.LID = r.JID.String()
		}
		out = append(out, pr)
	}
	return out, nil
}

// Approve admits the given requesters to a group. It pauses briefly first
// (humanized) so a batch of approvals doesn't land in one machine-instant
// flurry — the same anti-abuse hygiene as the send path.
//
// A per-participant refusal is an error, never a success: callers act on
// "approved" once and never revisit it, so claiming it wrongly strands someone
// outside forever. Claiming the reverse costs a retry that finds nothing to do.
func (c *Client) Approve(ctx context.Context, group types.JID, requesters []types.JID) error {
	if len(requesters) == 0 {
		return nil
	}
	approvePause(ctx)
	results, err := c.wm.UpdateGroupRequestParticipants(ctx, group, requesters, whatsmeow.ParticipantChangeApprove)
	if err != nil {
		return fmt.Errorf("approve participants: %w", err)
	}
	if err := participantErrors(results); err != nil {
		return fmt.Errorf("approve participants: %w", err)
	}
	return nil
}

// participantErrors reports whoever WhatsApp refused in its per-participant
// reply; Error == 0 means it attached no code, i.e. that one went through. It
// speaks only for participants WhatsApp mentioned — stated refusals, not silence.
//
// Defensive: whatsmeow documents these codes for *adding* participants, and no
// approve has been seen refused this way. Don't drop a safety net for it.
func participantErrors(results []types.GroupParticipant) error {
	var refused []string
	for _, p := range results {
		if p.Error != 0 {
			refused = append(refused, fmt.Sprintf("%s: error %d", p.JID, p.Error))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("refused by whatsapp: %s", strings.Join(refused, "; "))
}

// membersFrom resolves a participant list to Members (no network calls — phone
// comes from the participant's PhoneNumber, or a local LID-store lookup).
func (c *Client) membersFrom(ctx context.Context, parts []types.GroupParticipant) []Member {
	out := make([]Member, 0, len(parts))
	for _, p := range parts {
		phone := p.PhoneNumber.User
		if phone == "" {
			phone = c.ResolvePhone(ctx, p.JID)
		}
		out = append(out, Member{JID: p.JID, Phone: phone, IsAdmin: p.IsAdmin || p.IsSuperAdmin})
	}
	return out
}

// withRateRetry retries fn with exponential backoff on WhatsApp 429s.
func (c *Client) withRateRetry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err = fn(); err == nil || !isRateLimited(err) {
			return err
		}
		backoff := time.Duration(1<<attempt) * time.Second
		slog.Warn("whatsapp rate-limited, backing off", slog.Duration("wait", backoff))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func isRateLimited(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate-overlimit"))
}

// selfIsAdmin reports whether the bot is an admin of the given group.
func selfIsAdmin(info *types.GroupInfo, self, selfLID types.JID) bool {
	for _, p := range info.Participants {
		if participantIsSelf(p, self, selfLID) {
			return p.IsAdmin || p.IsSuperAdmin
		}
	}
	return false
}

// participantIsSelf matches the bot's own identity against a participant across
// its phone/LID forms — the bot may be addressed either way, so both of the
// bot's own forms are checked against each of the participant's forms.
//
// Each match requires the same JID Server, not just the same User. A phone
// number and a LID are different namespaces that happen to both be numeric, so
// on the User alone a stranger's LID can collide with the bot's phone number
// (or vice versa) and the bot would either skip a real member or flag itself.
func participantIsSelf(p types.GroupParticipant, self, selfLID types.JID) bool {
	if (self.IsEmpty() || self.User == "") && selfLID.IsEmpty() {
		return false
	}
	sameJID := func(a, b types.JID) bool {
		return !a.IsEmpty() && !b.IsEmpty() && a.User == b.User && a.Server == b.Server
	}
	switch {
	case !self.IsEmpty() && sameJID(p.JID, self):
		return true
	case !self.IsEmpty() && sameJID(p.PhoneNumber, self):
		return true
	case !selfLID.IsEmpty() && sameJID(p.JID, selfLID):
		return true
	case !selfLID.IsEmpty() && sameJID(p.LID, selfLID):
		return true
	}
	return false
}

func (c *Client) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Connected:
		slog.Info("whatsapp: connected")
		if c.onConnected != nil {
			go c.onConnected()
		}
	case *events.PairSuccess:
		slog.Info("whatsapp: paired successfully")
	case *events.LoggedOut:
		slog.Error("whatsapp: logged out / session lost — manual re-pair required",
			slog.Any("reason", v.Reason))
		if c.onLoggedOut != nil {
			go c.onLoggedOut(v.Reason.String())
		}
	case *events.Disconnected:
		slog.Warn("whatsapp: disconnected (auto-reconnect will retry)")
	case *events.Message:
		if c.onMessage != nil {
			c.onMessage(v)
		}
	}
}
