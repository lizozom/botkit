package bot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lizozom/botkit/webauth"
)

// Mounting two APIs on one port must not let either shadow the other: the
// pairing catch-all must not swallow /webauth/*, and the /webauth/ prefix must
// not capture /pair.
func TestOpsHandlerRouting(t *testing.T) {
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(name))
		})
	}
	h := opsHandler(mark("pairing"), mark("webauth"))

	cases := map[string]string{
		"/pair":             "pairing",
		"/status":           "pairing",
		"/groups":           "pairing",
		"/healthz":          "pairing",
		"/webauth/redeem":   "webauth",
		"/webauth/refresh":  "webauth",
		"/webauth/anything": "webauth",
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Body.String(); got != want {
			t.Errorf("%s routed to %q, want %q", path, got, want)
		}
	}
}

func TestOpsHandlerWithoutWebAuth(t *testing.T) {
	pairing := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pairing"))
	})
	h := opsHandler(pairing, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webauth/redeem", nil))
	if got := rec.Body.String(); got != "pairing" {
		t.Errorf("body = %q; with webauth off the pairing handler owns everything", got)
	}
}

// A misconfigured webauth must fail at New, not at someone's first login.
func TestNewRejectsBadWebAuthConfig(t *testing.T) {
	base := func() Config {
		return Config{
			SessionDBPath: "session.db",
			OpsAddr:       ":8080",
			WebAuth: &webauth.Config{
				DashboardURL: "https://dash.example.com",
				APIToken:     "api-token",
				SigningKey:   "signing-key",
			},
		}
	}

	if _, err := New(base()); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	missingKey := base()
	missingKey.WebAuth.SigningKey = ""
	if _, err := New(missingKey); err == nil {
		t.Error("New accepted a webauth config with no signing key")
	}

	sharedSecret := base()
	sharedSecret.WebAuth.SigningKey = sharedSecret.WebAuth.APIToken
	if _, err := New(sharedSecret); err == nil {
		t.Error("New accepted one secret used for both jobs")
	}

	// The endpoints live on the ops port; without one they would be unreachable.
	noOpsAddr := base()
	noOpsAddr.OpsAddr = ""
	if _, err := New(noOpsAddr); err == nil {
		t.Error("New accepted WebAuth with no OpsAddr")
	}
}
