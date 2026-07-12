// Command hello is botkit's smallest real demo: it opens a durable WhatsApp
// session, serves the private pairing ops API, and connects to WhatsApp for
// real. Pairing is manual — the bot never requests a code on its own.
//
// It does not yet reply to messages: OnMessage / Reply land in Phase 2. What it
// proves today is the full connection lifecycle — pair once, reconnect from the
// saved session forever after.
//
// Run:
//
//	cp cmd/hello/.env.example cmd/hello/.env   # fill in BOT_PHONE + PAIR_TOKEN
//	go run ./cmd/hello
//
// Then pair (first run only), from another shell:
//
//	curl -X POST -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8080/pair
//	# type the returned code into: WhatsApp > Linked Devices > Link a device >
//	# Link with phone number instead
//	curl -H "Authorization: Bearer $PAIR_TOKEN" http://localhost:8080/status
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/lizozom/botkit/pairing"
	"github.com/lizozom/botkit/telemetry"
	"github.com/lizozom/botkit/transport"
)

const version = "0.0.1-hello"

func main() {
	_ = godotenv.Load("cmd/hello/.env", ".env") // best-effort; env may be set another way

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Init(ctx, "botkit-hello", version)
	if err != nil {
		log.Fatalf("telemetry: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	phone := os.Getenv("BOT_PHONE")
	token := os.Getenv("PAIR_TOKEN")
	dbPath := envOr("SESSION_DB", "whatsapp_session.db")
	opsAddr := envOr("OPS_ADDR", ":8080")

	client, err := transport.New(ctx, dbPath)
	if err != nil {
		log.Fatalf("build whatsapp client: %v", err)
	}

	client.SetOnConnected(func() {
		slog.Info("hello, world — connected to WhatsApp", slog.String("self", client.SelfJID().String()))
	})
	client.SetOnLoggedOut(func(reason string) {
		slog.Error("session lost — re-pair via POST /pair (bot will NOT auto-pair)",
			slog.String("reason", reason))
	})

	// Pairing ops API. The adapter binds the configured phone and the PROCESS
	// context into Pair — never the HTTP request context, which would tear the
	// socket down the moment the response returns.
	ops := pairing.New(token, &pairAdapter{c: client, phone: phone, ctx: ctx})
	srv := &http.Server{Addr: opsAddr, Handler: ops.Handler()}
	go func() {
		slog.Info("ops API listening (keep it private — never expose this port)", slog.String("addr", opsAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("ops server", slog.String("err", err.Error()))
		}
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	// Go online with the saved session, or idle if unpaired. Never auto-pair.
	switch err := client.Connect(ctx); {
	case errors.Is(err, transport.ErrNotPaired):
		slog.Warn("not paired yet — trigger pairing manually",
			slog.String("how", fmt.Sprintf("curl -X POST -H 'Authorization: Bearer <PAIR_TOKEN>' http://localhost%s/pair", opsAddr)))
	case err != nil:
		log.Fatalf("connect: %v", err)
	default:
		slog.Info("connecting with saved session…")
	}

	<-ctx.Done()
	slog.Info("shutting down")
	client.Disconnect()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// pairAdapter bridges *transport.Client to pairing.WhatsApp: it binds the
// configured phone and the process-lifetime context, and maps GroupSummary to
// the API's Group shape.
type pairAdapter struct {
	c     *transport.Client
	phone string
	ctx   context.Context
}

func (a *pairAdapter) Paired() bool    { return a.c.Paired() }
func (a *pairAdapter) Connected() bool { return a.c.Connected() }

// Pair ignores the handler's request context on purpose — see the note above.
func (a *pairAdapter) Pair(context.Context) (string, error) {
	return a.c.Pair(a.ctx, a.phone)
}

func (a *pairAdapter) Groups(context.Context) ([]pairing.Group, error) {
	gs, err := a.c.AllGroups(a.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pairing.Group, 0, len(gs))
	for _, g := range gs {
		out = append(out, pairing.Group{
			JID:     g.JID.String(),
			Name:    g.Name,
			IsAdmin: g.IsAdmin,
			Members: len(g.Members),
		})
	}
	return out, nil
}
