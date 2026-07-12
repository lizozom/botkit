// Command hello is botkit's smallest real bot. It connects to WhatsApp (manual
// pairing, never auto-pairs), then replies "pong" to any "ping" in a managed
// group, and nudges DMs from members back to the group.
//
// Run:
//
//	cp cmd/hello/.env.example cmd/hello/.env   # fill in BOT_PHONE + PAIR_TOKEN
//	go run ./cmd/hello
//
// First run only, pair from another shell:
//
//	curl -X POST -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8099/pair
//	# enter the code: WhatsApp > Linked Devices > Link a device >
//	# Link with phone number instead
//
// Discover a group JID for MANAGED_GROUPS, then set it and restart:
//
//	curl -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8099/groups
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/lizozom/botkit/bot"
	"github.com/lizozom/botkit/telemetry"
)

const version = "0.0.2-hello"

func main() {
	_ = godotenv.Load("cmd/hello/.env", ".env") // best-effort

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "botkit-hello", version)
	if err != nil {
		log.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	b, err := bot.New(bot.Config{
		SessionDBPath: envOr("SESSION_DB", "whatsapp_session.db"),
		BotPhone:      os.Getenv("BOT_PHONE"),
		ManagedGroups: splitList(os.Getenv("MANAGED_GROUPS")),
		OpsAddr:       envOr("OPS_ADDR", ":8099"),
		OpsToken:      os.Getenv("PAIR_TOKEN"),
	})
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	// Reply "pong" to "ping" in a managed group. Reactive, low-noise —
	// it ignores everything else rather than chattering on every message.
	b.OnGroupMessage(func(ctx context.Context, msg bot.InboundMessage) error {
		if strings.EqualFold(strings.TrimSpace(msg.Text), "ping") {
			slog.Info("ping received", slog.String("from", msg.SenderName))
			return msg.Reply(ctx, "pong 🏓 — botkit is alive")
		}
		return nil
	})

	// DMs, only from people already in a managed group — nudge them to the group.
	b.OnDirectMessage(bot.DMsFromMembers, func(ctx context.Context, msg bot.InboundMessage) error {
		return msg.Reply(ctx, "hey — talk to me in the group 🙂")
	})

	if err := b.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
