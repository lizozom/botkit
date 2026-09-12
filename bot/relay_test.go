package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lizozom/botkit/send"
)

func TestNewParsesRelayTarget(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		wantErr bool
		wantJID string
	}{
		{name: "group", target: "12036@g.us", wantJID: "12036@g.us"},
		{name: "contact", target: "972501234567@s.whatsapp.net", wantJID: "972501234567@s.whatsapp.net"},
		{name: "empty disables", target: "", wantJID: ""},
		{name: "whitespace disables", target: "   ", wantJID: ""},
		{name: "server only", target: "@g.us", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := New(Config{SessionDBPath: "x.db", RelayTarget: tc.target})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got none", tc.target)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got := ""
			if !b.RelayTarget().IsEmpty() {
				got = b.RelayTarget().String()
			}
			if got != tc.wantJID {
				t.Fatalf("target = %q, want %q", got, tc.wantJID)
			}
		})
	}
}

func TestRelayDisabledWithoutTarget(t *testing.T) {
	b, err := New(Config{SessionDBPath: "x.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.relay(context.Background(), OutboundMedia{Data: []byte("x"), MIME: "image/jpeg"}, ""); !errors.Is(err, ErrRelayDisabled) {
		t.Fatalf("want ErrRelayDisabled, got %v", err)
	}
	// A message built by a relay-less bot carries no relay closure either.
	var m InboundMessage
	if err := m.Relay(context.Background(), OutboundMedia{}, ""); !errors.Is(err, ErrRelayDisabled) {
		t.Fatalf("want ErrRelayDisabled from unbound message, got %v", err)
	}
}

func TestRelayDefaultCap(t *testing.T) {
	b, err := New(Config{SessionDBPath: "x.db", RelayTarget: "12036@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	if b.relayLimit.cap != DefaultRelayDailyCap {
		t.Fatalf("cap = %d, want %d", b.relayLimit.cap, DefaultRelayDailyCap)
	}
}

func TestRelayLimiterCapAndRollover(t *testing.T) {
	l := &relayLimiter{cap: 2}
	day1 := time.Date(2026, 9, 12, 23, 0, 0, 0, time.UTC)

	for i := range 2 {
		if err := l.take(day1); err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
	}
	if err := l.take(day1); !errors.Is(err, ErrRelayCapReached) {
		t.Fatalf("want ErrRelayCapReached, got %v", err)
	}

	// Crossing into the next UTC day refills the budget.
	if err := l.take(day1.Add(2 * time.Hour)); err != nil {
		t.Fatalf("after rollover: %v", err)
	}
}

func TestRelayLimiterHaltIsBotWideAndClears(t *testing.T) {
	l := &relayLimiter{cap: 10}
	now := time.Now()

	l.halt()
	err := l.take(now)
	if !errors.Is(err, send.ErrBotWide) {
		t.Fatalf("want ErrBotWide while halted, got %v", err)
	}
	// A halt must not be mistaken for an exhausted budget.
	if errors.Is(err, ErrRelayCapReached) {
		t.Fatal("halt must not report as cap reached")
	}

	l.resume()
	if err := l.take(now); err != nil {
		t.Fatalf("after resume: %v", err)
	}
}

func TestRelayRejectsEmptyPayload(t *testing.T) {
	b, err := New(Config{SessionDBPath: "x.db", RelayTarget: "12036@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.relay(ctx, OutboundMedia{MIME: "image/jpeg"}, ""); err == nil {
		t.Fatal("want error for empty data")
	}
	if err := b.relay(ctx, OutboundMedia{Data: []byte("x")}, ""); err == nil {
		t.Fatal("want error for missing MIME")
	}
	// Neither rejection should have spent budget.
	if b.relayLimit.count != 0 {
		t.Fatalf("count = %d, want 0", b.relayLimit.count)
	}
}
