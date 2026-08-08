package webauth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lizozom/botkit/store"
)

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"no dashboard URL", func(c *Config) { c.DashboardURL = "" }, true},
		{"relative dashboard URL", func(c *Config) { c.DashboardURL = "/auth" }, true},
		{"scheme-less dashboard URL", func(c *Config) { c.DashboardURL = "dash.example.com" }, true},
		{"no API token", func(c *Config) { c.APIToken = "" }, true},
		{"no signing key", func(c *Config) { c.SigningKey = "" }, true},
		// One value doing both jobs means a leaked API token also forges sessions.
		{"shared secret", func(c *Config) { c.SigningKey = c.APIToken }, true},
		{"recheck longer than session", func(c *Config) {
			c.SessionTTL = time.Hour
			c.RecheckInterval = 2 * time.Hour
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	got, err := testConfig().withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if got.LinkTTL != DefaultLinkTTL {
		t.Errorf("LinkTTL = %v, want %v", got.LinkTTL, DefaultLinkTTL)
	}
	if got.SessionTTL != DefaultSessionTTL {
		t.Errorf("SessionTTL = %v, want %v", got.SessionTTL, DefaultSessionTTL)
	}
	if got.RecheckInterval != DefaultRecheckInterval {
		t.Errorf("RecheckInterval = %v, want %v", got.RecheckInterval, DefaultRecheckInterval)
	}
	if !got.singleUse() {
		t.Error("links must be single-use unless explicitly opted out")
	}
}

func TestDashboardURLTrailingSlashTrimmed(t *testing.T) {
	cfg := testConfig()
	cfg.DashboardURL = "https://dash.example.com/"
	a, _ := newAuth(t, cfg, newMembership(member))

	link, err := a.MintLink(t.Context(), group, member)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://dash.example.com/auth?t="; link[:len(want)] != want {
		t.Errorf("link = %q, want no doubled slash", link)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	kv, err := store.NewKV(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()

	if _, err := New(testConfig(), nil, newMembership(member)); err == nil {
		t.Error("New accepted a nil KV")
	}
	if _, err := New(testConfig(), kv, nil); err == nil {
		t.Error("New accepted a nil Membership — nothing would authorize the login")
	}
}
