package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example-org/workspace/broker/internal/github"
	"github.com/example-org/workspace/broker/internal/identity"
	"github.com/example-org/workspace/broker/internal/rules"
	"github.com/example-org/workspace/broker/internal/server"
	"github.com/example-org/workspace/broker/internal/stops"
)

// tokens maps a bearer token to the identity it proves.
type tokens map[string]identity.ID

func (t tokens) Verify(_ context.Context, raw string) (identity.ID, error) {
	if id, ok := t[raw]; ok {
		return id, nil
	}
	return identity.ID{}, errors.New("bad token")
}

type staticRules rules.Set

func (s staticRules) Current() (rules.Set, error) { return rules.Set(s), nil }

type fakeMinter struct{ got *github.Request }

func (f fakeMinter) Mint(_ context.Context, _ github.Signer, req github.Request) (github.Token, error) {
	*f.got = req
	return github.Token{Token: "ghs_fake", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type harness struct {
	server *server.Server
	srv    *httptest.Server
	stops  *stops.Memory
	minted github.Request
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{stops: stops.NewMemory()}
	s := &server.Server{
		Verifier: tokens{
			"ada":         {Kind: "agent", Name: "ada"},
			"grace":       {Kind: "agent", Name: "grace"},
			"max":         {Kind: "agent", Name: "max"},
			"alex-mac":    {Kind: "launcher", Name: "alex.mac"},
			"colleague-l": {Kind: "launcher", Name: "colleague.mac"},
		},
		Rules: staticRules{Agents: map[string]rules.Agent{
			"ada": {Status: "active", Placement: "alex.mac", GitHub: rules.GitHub{
				AppID: 101, InstallationID: 202, Key: "file:unused",
				Repositories: []string{"example-org/workspace-sandbox"},
				Permissions:  map[string]string{"contents": "write", "pull_requests": "write"},
			}},
			"grace": {Status: "suspended", Placement: "colleague.mac"},
			"max":   {Status: "active", Placement: "colleague.mac"},
		}},
		Stops:     h.stops,
		Minter:    fakeMinter{got: &h.minted},
		SignerFor: func(string) (github.Signer, error) { return nil, nil },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.server = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) post(t *testing.T, path, token string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+path, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func wantRefused(t *testing.T, status int, out map[string]any, wantStatus int, reason string) {
	t.Helper()
	if status != wantStatus || out["reason"] != reason {
		t.Fatalf("got %d %v, want %d reason %q", status, out, wantStatus, reason)
	}
}

func TestLauncherCheckAllowsAnAgentOnItsOwnLaptop(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/launcher/check", "alex-mac", map[string]string{"agent": "ada"})
	if status != http.StatusOK || out["allowed"] != true {
		t.Fatalf("got %d %v", status, out)
	}
}

func TestLauncherCheckRefusesAnAgentPlacedOnAnotherLaptop(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/launcher/check", "alex-mac", map[string]string{"agent": "max"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonWrongPlacement)
}

func TestLauncherCheckRefusesAnInactiveAgent(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/launcher/check", "colleague-l", map[string]string{"agent": "grace"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonNotActive)
}

func TestLauncherCheckRefusesAStoppedAgent(t *testing.T) {
	h := newHarness(t)
	_ = h.stops.Set(t.Context(), stops.Stop{Agent: "ada", By: "alex", Reason: "test"})
	status, out := h.post(t, "/v1/launcher/check", "alex-mac", map[string]string{"agent": "ada"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonStopped)
}

func TestLauncherCheckRefusesAnAgentIdentity(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/launcher/check", "ada", map[string]string{"agent": "ada"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonWrongCaller)
}

func TestRequestsWithoutAValidSVIDAreRefused(t *testing.T) {
	h := newHarness(t)
	for _, tok := range []string{"", "forged"} {
		status, out := h.post(t, "/v1/github/token", tok, map[string]string{"repository": "example-org/workspace-sandbox"})
		wantRefused(t, status, out, http.StatusUnauthorized, server.ReasonUnauthenticated)
	}
}

func TestGitHubTokenIsMintedForTheAgentsOwnRepositoryOnly(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/github/token", "ada", map[string]string{"repository": "example-org/workspace-sandbox"})
	if status != http.StatusOK || out["token"] != "ghs_fake" {
		t.Fatalf("got %d %v", status, out)
	}
	if h.minted.AppID != 101 || len(h.minted.Repositories) != 1 || h.minted.Repositories[0] != "example-org/workspace-sandbox" {
		t.Fatalf("minted %+v, want ada's App limited to the one repository", h.minted)
	}
	if h.minted.Permissions["contents"] != "write" {
		t.Fatalf("permissions %v", h.minted.Permissions)
	}
}

func TestGitHubTokenRefusesAnotherRepository(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/github/token", "ada", map[string]string{"repository": "example-org/workspace"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonRepositoryNotAllowed)
}

func TestGitHubTokenRefusesAStoppedAgent(t *testing.T) {
	h := newHarness(t)
	_ = h.stops.Set(t.Context(), stops.Stop{Agent: "ada", By: "alex", Reason: "test"})
	status, out := h.post(t, "/v1/github/token", "ada", map[string]string{"repository": "example-org/workspace-sandbox"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonStopped)
}

func TestGitHubTokenRefusesAnAgentWithoutAnApp(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/github/token", "max", map[string]string{"repository": "example-org/workspace-sandbox"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonNoGitHubApp)
}

func TestGitHubTokenRefusesALauncherIdentity(t *testing.T) {
	h := newHarness(t)
	status, out := h.post(t, "/v1/github/token", "alex-mac", map[string]string{"repository": "example-org/workspace-sandbox"})
	wantRefused(t, status, out, http.StatusForbidden, server.ReasonWrongCaller)
}

type brokenRules struct{}

func (brokenRules) Current() (rules.Set, error) { return rules.Set{}, errors.New("parse rules: bad") }

type brokenStops struct{ stops.Memory }

func (*brokenStops) Stopped(context.Context, string) (stops.Stop, bool, error) {
	return stops.Stop{}, false, errors.New("database down")
}

func (h *harness) health(t *testing.T) int {
	t.Helper()
	resp, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestBrokenRulesRefuseEveryRequest(t *testing.T) {
	h := newHarness(t)
	h.server.Rules = brokenRules{}
	status, out := h.post(t, "/v1/launcher/check", "alex-mac", map[string]string{"agent": "ada"})
	wantRefused(t, status, out, http.StatusServiceUnavailable, server.ReasonRulesUnavailable)
	status, out = h.post(t, "/v1/github/token", "ada", map[string]string{"repository": "example-org/workspace-sandbox"})
	wantRefused(t, status, out, http.StatusServiceUnavailable, server.ReasonRulesUnavailable)
}

func TestAnUnavailableStopSwitchRefusesWithItsOwnReason(t *testing.T) {
	h := newHarness(t)
	h.server.Stops = &brokenStops{}
	status, out := h.post(t, "/v1/launcher/check", "alex-mac", map[string]string{"agent": "ada"})
	wantRefused(t, status, out, http.StatusServiceUnavailable, server.ReasonStopsUnavailable)
}

func TestHealthReportsWhetherTheBrokerCanDecide(t *testing.T) {
	h := newHarness(t)
	if got := h.health(t); got != http.StatusOK {
		t.Fatalf("healthy broker: %d", got)
	}
	h.server.Rules = brokenRules{}
	if got := h.health(t); got != http.StatusServiceUnavailable {
		t.Fatalf("broken rules: %d", got)
	}
	h.server.Rules = newHarness(t).server.Rules
	h.server.Stops = &brokenStops{}
	if got := h.health(t); got != http.StatusServiceUnavailable {
		t.Fatalf("broken stop switch: %d", got)
	}
}
