package webauth

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lizozom/botkit/store"
	"go.mau.fi/whatsmeow/types"
)

var (
	group   = types.JID{User: "12036", Server: types.GroupServer}
	member  = types.JID{User: "972546260906", Server: types.DefaultUserServer}
	lidOnly = types.JID{User: "11223344", Server: types.HiddenUserServer}
	other   = types.JID{User: "972500000000", Server: types.DefaultUserServer}
)

// fakeMembership answers IsMember from an in-memory set, and can be flipped to
// simulate someone leaving the group or WhatsApp being unreachable.
type fakeMembership struct {
	mu      sync.Mutex
	members map[string]bool
	err     error
	calls   int
}

func newMembership(in ...types.JID) *fakeMembership {
	f := &fakeMembership{members: map[string]bool{}}
	for _, j := range in {
		f.members[j.String()] = true
	}
	return f
}

func (f *fakeMembership) IsMember(_ context.Context, _, m types.JID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.members[m.String()], nil
}

func (f *fakeMembership) remove(j types.JID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.members, j.String())
}

func (f *fakeMembership) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func testConfig() Config {
	return Config{
		DashboardURL: "https://dash.example.com",
		APIToken:     "api-token-value",
		SigningKey:   "signing-key-value",
	}
}

// newAuth builds an Auth over a temp KV with a controllable clock.
func newAuth(t *testing.T, cfg Config, mem Membership) (*Auth, *time.Time) {
	t.Helper()
	kv, err := store.NewKV(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewKV: %v", err)
	}
	t.Cleanup(func() { _ = kv.Close() })

	a, err := New(cfg, kv, mem)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	a.clock = func() time.Time { return now }
	return a, &now
}

func mustMint(t *testing.T, a *Auth, g, m types.JID) string {
	t.Helper()
	link, err := a.MintLink(context.Background(), g, m)
	if err != nil {
		t.Fatalf("MintLink: %v", err)
	}
	return nonceOf(t, link)
}

func nonceOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	n := u.Query().Get("t")
	if n == "" {
		t.Fatalf("link %q carries no nonce", link)
	}
	return n
}

func TestHappyPath(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))

	link, err := a.MintLink(ctx, group, member)
	if err != nil {
		t.Fatalf("MintLink: %v", err)
	}
	if !strings.HasPrefix(link, "https://dash.example.com/auth?t=") {
		t.Errorf("link = %q, want the configured dashboard /auth URL", link)
	}

	token, err := a.Redeem(ctx, nonceOf(t, link))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	claims, err := a.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Group != group.String() || claims.Member != member.String() {
		t.Errorf("claims = %+v; want group/member from the link", claims)
	}
	if claims.Expires-claims.IssuedAt != int64(DefaultRecheckInterval.Seconds()) {
		t.Errorf("token lifetime = %ds, want RecheckInterval", claims.Expires-claims.IssuedAt)
	}
	if claims.Absolute-claims.IssuedAt != int64(DefaultSessionTTL.Seconds()) {
		t.Errorf("ceiling = %ds after issue, want SessionTTL", claims.Absolute-claims.IssuedAt)
	}
}

// A LID-only participant — someone whose phone the bot cannot see — must be
// able to log in. Keying on phone rather than JID would lock them out.
func TestLIDOnlyMemberCanRedeem(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(lidOnly))

	if _, err := a.Redeem(ctx, mustMint(t, a, group, lidOnly)); err != nil {
		t.Fatalf("LID-only member denied: %v", err)
	}
}

func TestLinkIsSingleUse(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	nonce := mustMint(t, a, group, member)

	if _, err := a.Redeem(ctx, nonce); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := a.Redeem(ctx, nonce); !errors.Is(err, ErrDenied) {
		t.Errorf("second redeem err = %v; want ErrDenied", err)
	}
}

// Concurrent taps of one link must yield exactly one session.
func TestConcurrentRedeemYieldsOneSession(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	nonce := mustMint(t, a, group, member)

	const racers = 12
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := a.Redeem(ctx, nonce); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("successful redemptions = %d; want exactly 1", wins)
	}
}

func TestExpiredLinkDenied(t *testing.T) {
	ctx := context.Background()
	a, now := newAuth(t, testConfig(), newMembership(member))
	nonce := mustMint(t, a, group, member)

	*now = now.Add(DefaultLinkTTL + time.Second)
	if _, err := a.Redeem(ctx, nonce); !errors.Is(err, ErrDenied) {
		t.Errorf("expired link err = %v; want ErrDenied", err)
	}
}

// The core guarantee: holding a valid, live, unused nonce is not enough.
func TestNonMemberDeniedWithValidNonce(t *testing.T) {
	ctx := context.Background()
	mem := newMembership(member)
	a, _ := newAuth(t, testConfig(), mem)
	nonce := mustMint(t, a, group, member)

	mem.remove(member) // removed from the group between mint and redeem
	if _, err := a.Redeem(ctx, nonce); !errors.Is(err, ErrDenied) {
		t.Errorf("redeem by ex-member err = %v; want ErrDenied", err)
	}
}

// An unreachable WhatsApp must not become an open door.
func TestMembershipErrorDenies(t *testing.T) {
	ctx := context.Background()
	mem := newMembership(member)
	a, _ := newAuth(t, testConfig(), mem)
	nonce := mustMint(t, a, group, member)

	mem.fail(errors.New("whatsapp unreachable"))
	if _, err := a.Redeem(ctx, nonce); !errors.Is(err, ErrDenied) {
		t.Errorf("err = %v; want ErrDenied when the membership query fails", err)
	}
}

func TestUnknownNonceDenied(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, testConfig(), newMembership(member))
	for _, n := range []string{"", "not-a-nonce", strings.Repeat("A", 43)} {
		if _, err := a.Redeem(ctx, n); !errors.Is(err, ErrDenied) {
			t.Errorf("Redeem(%q) err = %v; want ErrDenied", n, err)
		}
	}
}

func TestReusableLinkMode(t *testing.T) {
	ctx := context.Background()
	reusable := false
	cfg := testConfig()
	cfg.LinkSingleUse = &reusable
	a, _ := newAuth(t, cfg, newMembership(member))
	nonce := mustMint(t, a, group, member)

	for i := 0; i < 3; i++ {
		if _, err := a.Redeem(ctx, nonce); err != nil {
			t.Fatalf("redeem %d with LinkSingleUse=false: %v", i, err)
		}
	}
	// Still membership-gated, reusable or not.
	a.mem.(*fakeMembership).remove(member)
	if _, err := a.Redeem(ctx, nonce); !errors.Is(err, ErrDenied) {
		t.Error("reusable link stayed valid after the member left")
	}
}

func TestRefreshRechecksMembership(t *testing.T) {
	ctx := context.Background()
	mem := newMembership(member)
	a, now := newAuth(t, testConfig(), mem)

	token, err := a.Redeem(ctx, mustMint(t, a, group, member))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	// An hour on, still a member: refresh succeeds.
	*now = now.Add(DefaultRecheckInterval + time.Minute)
	refreshed, err := a.Refresh(ctx, token)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := a.Verify(refreshed); err != nil {
		t.Errorf("refreshed token does not verify: %v", err)
	}

	// Another hour on, removed from the group: refresh denies.
	*now = now.Add(DefaultRecheckInterval + time.Minute)
	mem.remove(member)
	if _, err := a.Refresh(ctx, refreshed); !errors.Is(err, ErrDenied) {
		t.Error("refresh succeeded for someone no longer in the group")
	}
}

// Refresh must not extend a login past SessionTTL.
func TestRefreshStopsAtAbsoluteCeiling(t *testing.T) {
	ctx := context.Background()
	a, now := newAuth(t, testConfig(), newMembership(member))

	token, err := a.Redeem(ctx, mustMint(t, a, group, member))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	origin, err := a.parse(token)
	if err != nil {
		t.Fatal(err)
	}

	// Walk forward an hour at a time; every refresh must keep the same ceiling.
	for step := 0; step < 10; step++ {
		*now = now.Add(DefaultRecheckInterval)
		next, err := a.Refresh(ctx, token)
		if err != nil {
			t.Fatalf("refresh at step %d: %v", step, err)
		}
		c, err := a.parse(next)
		if err != nil {
			t.Fatal(err)
		}
		if c.Absolute != origin.Absolute {
			t.Fatalf("ceiling moved: %d -> %d", origin.Absolute, c.Absolute)
		}
		token = next
	}

	*now = time.Unix(origin.Absolute, 0).Add(time.Second)
	if _, err := a.Refresh(ctx, token); !errors.Is(err, ErrDenied) {
		t.Error("refresh succeeded past the absolute ceiling")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	ctx := context.Background()
	a, now := newAuth(t, testConfig(), newMembership(member))
	token, err := a.Redeem(ctx, mustMint(t, a, group, member))
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(DefaultRecheckInterval + time.Second)
	if _, err := a.Verify(token); !errors.Is(err, ErrDenied) {
		t.Error("Verify accepted an expired token")
	}
}

// Unredeemed nonces must not pile up in the KV forever. The sweep keys off the
// row's updated_at, which is wall-clock and second-resolution, so this one test
// moves real time rather than the injected clock.
func TestMintSweepsExpiredNonces(t *testing.T) {
	if testing.Short() {
		t.Skip("sleeps past a one-second SQLite timestamp boundary")
	}
	ctx := context.Background()
	cfg := testConfig()
	cfg.LinkTTL = time.Second
	a, _ := newAuth(t, cfg, newMembership(member))

	stale := mustMint(t, a, group, member)
	time.Sleep(2100 * time.Millisecond)
	fresh := mustMint(t, a, group, member) // this mint triggers the sweep

	if _, ok, _ := a.kv.Get(ctx, noncePrefix+stale); ok {
		t.Error("expired nonce survived the sweep")
	}
	if _, ok, _ := a.kv.Get(ctx, noncePrefix+fresh); !ok {
		t.Error("the sweep ate a live nonce")
	}
}
