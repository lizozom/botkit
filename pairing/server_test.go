package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeWA struct {
	paired    bool
	connected bool
	code      string
	pairErr   error
	pairCalls int
	groups    []Group
}

func (f *fakeWA) Paired() bool    { return f.paired }
func (f *fakeWA) Connected() bool { return f.connected }
func (f *fakeWA) Pair(context.Context) (string, error) {
	f.pairCalls++
	return f.code, f.pairErr
}
func (f *fakeWA) Groups(context.Context) ([]Group, error) { return f.groups, nil }

func request(t *testing.T, h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPairRequiresToken(t *testing.T) {
	h := New("secret", &fakeWA{}).Handler()
	if rec := request(t, h, http.MethodPost, "/pair", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, "/pair", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", rec.Code)
	}
}

func TestAPIDisabledWithoutConfiguredToken(t *testing.T) {
	fwa := &fakeWA{code: "ABCD-1234"}
	h := New("", fwa).Handler()
	if rec := request(t, h, http.MethodPost, "/pair", ""); rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403 when token unset", rec.Code)
	}
	if fwa.pairCalls != 0 {
		t.Error("Pair must never run when the API is disabled")
	}
}

func TestPairRejectsGET(t *testing.T) {
	fwa := &fakeWA{code: "ABCD-1234"}
	h := New("secret", fwa).Handler()
	if rec := request(t, h, http.MethodGet, "/pair", "secret"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /pair: got %d, want 405", rec.Code)
	}
	if fwa.pairCalls != 0 {
		t.Error("a GET must never trigger pairing")
	}
}

func TestPairAlreadyPaired(t *testing.T) {
	fwa := &fakeWA{paired: true}
	h := New("secret", fwa).Handler()
	if rec := request(t, h, http.MethodPost, "/pair", "secret"); rec.Code != http.StatusConflict {
		t.Errorf("got %d, want 409 when already paired", rec.Code)
	}
	if fwa.pairCalls != 0 {
		t.Error("Pair must not run when already paired")
	}
}

func TestPairReturnsCode(t *testing.T) {
	fwa := &fakeWA{code: "ABCD-1234"}
	h := New("secret", fwa).Handler()
	rec := request(t, h, http.MethodPost, "/pair", "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["pairing_code"] != "ABCD-1234" {
		t.Errorf("pairing_code = %q, want ABCD-1234", body["pairing_code"])
	}
}

func TestPairCooldown(t *testing.T) {
	fwa := &fakeWA{code: "ABCD-1234"}
	srv := New("secret", fwa)
	now := time.Unix(1000, 0)
	srv.clock = func() time.Time { return now }
	h := srv.Handler()

	if rec := request(t, h, http.MethodPost, "/pair", "secret"); rec.Code != http.StatusOK {
		t.Fatalf("first attempt: got %d, want 200", rec.Code)
	}
	if rec := request(t, h, http.MethodPost, "/pair", "secret"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("immediate retry: got %d, want 429", rec.Code)
	}
	if fwa.pairCalls != 1 {
		t.Errorf("Pair ran %d times, want 1 (cooldown must block the retry)", fwa.pairCalls)
	}

	now = now.Add(pairCooldown + time.Second)
	if rec := request(t, h, http.MethodPost, "/pair", "secret"); rec.Code != http.StatusOK {
		t.Errorf("after cooldown: got %d, want 200", rec.Code)
	}
}

func TestPairErrorSurfaced(t *testing.T) {
	fwa := &fakeWA{pairErr: errors.New("socket timeout")}
	h := New("secret", fwa).Handler()
	if rec := request(t, h, http.MethodPost, "/pair", "secret"); rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 on Pair error", rec.Code)
	}
}

func TestGroups(t *testing.T) {
	fwa := &fakeWA{paired: true, groups: []Group{{JID: "123@g.us", Name: "test", IsAdmin: true, Members: 4}}}
	h := New("secret", fwa).Handler()

	rec := request(t, h, http.MethodGet, "/groups", "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got []Group
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].JID != "123@g.us" || !got[0].IsAdmin {
		t.Errorf("groups = %+v", got)
	}
	if rec := request(t, h, http.MethodGet, "/groups", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /groups: got %d, want 401", rec.Code)
	}
}

func TestGroupsRequiresPaired(t *testing.T) {
	h := New("secret", &fakeWA{paired: false}).Handler()
	if rec := request(t, h, http.MethodGet, "/groups", "secret"); rec.Code != http.StatusConflict {
		t.Errorf("got %d, want 409 when not paired", rec.Code)
	}
}

func TestStatus(t *testing.T) {
	fwa := &fakeWA{paired: true, connected: false}
	h := New("secret", fwa).Handler()
	rec := request(t, h, http.MethodGet, "/status", "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body["paired"] || body["connected"] {
		t.Errorf("status = %v, want paired=true connected=false", body)
	}
	if rec := request(t, h, http.MethodGet, "/status", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /status: got %d, want 401", rec.Code)
	}
}
