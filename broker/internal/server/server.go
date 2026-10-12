// Package server is the credential service's HTTP API, behind agentgateway.
//
// Every request names its caller with a JWT-SVID (audience "broker"). Every
// decision is logged with the caller, the agent and a reason code, and every
// refusal returns that reason code, so a refused run can say why.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/example-org/workspace/broker/internal/github"
	"github.com/example-org/workspace/broker/internal/identity"
	"github.com/example-org/workspace/broker/internal/rules"
	"github.com/example-org/workspace/broker/internal/stops"
)

// Reason codes. They are part of the API: launchers and helpers show them.
const (
	ReasonUnauthenticated      = "unauthenticated"
	ReasonWrongCaller          = "wrong_caller"
	ReasonUnknownAgent         = "unknown_agent"
	ReasonNotActive            = "not_active"
	ReasonStopped              = "stopped"
	ReasonStopsUnavailable     = "stops_unavailable"
	ReasonWrongPlacement       = "wrong_placement"
	ReasonRepositoryNotAllowed = "repository_not_allowed"
	ReasonNoGitHubApp          = "no_github_app"
	ReasonRulesUnavailable     = "rules_unavailable"
	ReasonUpstream             = "upstream_error"
	ReasonBadRequest           = "bad_request"
)

type Verifier interface {
	Verify(ctx context.Context, raw string) (identity.ID, error)
}

type Rules interface {
	Current() (rules.Set, error)
}

type Minter interface {
	Mint(ctx context.Context, signer github.Signer, req github.Request) (github.Token, error)
}

// SignerFor returns the Signer for a key reference from the rules
// ("file:<path>" locally, "kms:<key>" in the cloud).
type SignerFor func(key string) (github.Signer, error)

type Server struct {
	Verifier  Verifier
	Rules     Rules
	Stops     stops.Store
	Minter    Minter
	SignerFor SignerFor
	Log       *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/launcher/check", s.launcherCheck)
	mux.HandleFunc("POST /v1/github/token", s.githubToken)
	return mux
}

// healthz reports whether the broker can decide: the rules load and the stop
// switch answers. While either fails, every request is refused.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	problem := ""
	if _, err := s.Rules.Current(); err != nil {
		problem = "rules: " + err.Error()
	} else if _, _, err := s.Stops.Stopped(r.Context(), "healthz"); err != nil {
		problem = "stop switch: " + err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	if problem != "" {
		s.Log.ErrorContext(r.Context(), "unhealthy", "problem", problem)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy", "problem": problem})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type refusal struct {
	status int
	reason string
	detail string
}

func refuse(status int, reason, detail string) *refusal {
	return &refusal{status: status, reason: reason, detail: detail}
}

// caller checks the JWT-SVID and that the caller is of the expected kind.
func (s *Server) caller(r *http.Request, kind string) (identity.ID, *refusal) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return identity.ID{}, refuse(http.StatusUnauthorized, ReasonUnauthenticated, "no JWT-SVID in the Authorization header")
	}
	id, err := s.Verifier.Verify(r.Context(), raw)
	if err != nil {
		return identity.ID{}, refuse(http.StatusUnauthorized, ReasonUnauthenticated, err.Error())
	}
	if id.Kind != kind {
		return id, refuse(http.StatusForbidden, ReasonWrongCaller, "this endpoint is for "+kind+" identities, not "+id.String())
	}
	return id, nil
}

// activeAgent returns the agent's rules if it may get anything at all: it is
// known, active and not stopped.
func (s *Server) activeAgent(ctx context.Context, name string) (rules.Agent, *refusal) {
	set, err := s.Rules.Current()
	if err != nil {
		return rules.Agent{}, refuse(http.StatusServiceUnavailable, ReasonRulesUnavailable, err.Error())
	}
	a, ok := set.Agents[name]
	if !ok {
		return a, refuse(http.StatusForbidden, ReasonUnknownAgent, "no agent "+name+" in the rules")
	}
	if !a.Active() {
		return a, refuse(http.StatusForbidden, ReasonNotActive, "agent "+name+" is "+a.Status)
	}
	stop, stopped, err := s.Stops.Stopped(ctx, name)
	if err != nil {
		// Fail closed: without the stop switch we cannot know.
		return a, refuse(http.StatusServiceUnavailable, ReasonStopsUnavailable, err.Error())
	}
	if stopped {
		return a, refuse(http.StatusForbidden, ReasonStopped, "stopped by "+stop.By+": "+stop.Reason)
	}
	return a, nil
}

// decode reads the JSON body into v and refuses it if the field it needs is empty.
func decode(r *http.Request, v any, field *string, shape string) *refusal {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil || *field == "" {
		return refuse(http.StatusBadRequest, ReasonBadRequest, "body must be "+shape)
	}
	return nil
}

// launcherCheck answers the launcher's question before it starts a run
// (ARCHITECTURE.md D50): is this agent active, not stopped, and placed on the
// laptop asking? The answer comes from here, not from the laptop.
func (s *Server) launcherCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Agent string `json:"agent"`
	}
	caller, ref := s.caller(r, "launcher")
	if ref == nil {
		ref = decode(r, &req, &req.Agent, `{"agent":"<name>"}`)
	}
	if ref == nil {
		ref = s.placedOn(r.Context(), req.Agent, caller.Name)
	}
	s.decide(w, r, "launcher.check", caller, req.Agent, ref, map[string]any{"allowed": true, "agent": req.Agent})
}

func (s *Server) placedOn(ctx context.Context, agent, laptop string) *refusal {
	a, ref := s.activeAgent(ctx, agent)
	if ref != nil {
		return ref
	}
	if a.Placement != laptop {
		return refuse(http.StatusForbidden, ReasonWrongPlacement, "agent "+agent+" runs on "+a.Placement+", not "+laptop)
	}
	return nil
}

// githubToken mints a one-hour token with the agent's own GitHub App, for one
// repository in its rules and only the permissions its rules list.
func (s *Server) githubToken(w http.ResponseWriter, r *http.Request) {
	caller, ref := s.caller(r, "agent")
	var tok github.Token
	if ref == nil {
		tok, ref = s.mintFor(r, caller.Name)
	}
	s.decide(w, r, "github.token", caller, caller.Name, ref, tok)
}

func (s *Server) mintFor(r *http.Request, agent string) (github.Token, *refusal) {
	var req struct {
		Repository string `json:"repository"`
	}
	if ref := decode(r, &req, &req.Repository, `{"repository":"<owner>/<name>"}`); ref != nil {
		return github.Token{}, ref
	}
	a, ref := s.activeAgent(r.Context(), agent)
	if ref != nil {
		return github.Token{}, ref
	}
	if a.GitHub.AppID == 0 {
		return github.Token{}, refuse(http.StatusForbidden, ReasonNoGitHubApp, "agent "+agent+" has no GitHub App")
	}
	if !slices.Contains(a.GitHub.Repositories, req.Repository) {
		return github.Token{}, refuse(http.StatusForbidden, ReasonRepositoryNotAllowed, req.Repository+" is not in the rules of "+agent)
	}
	return s.mint(r.Context(), a, req.Repository)
}

func (s *Server) mint(ctx context.Context, a rules.Agent, repo string) (github.Token, *refusal) {
	signer, err := s.SignerFor(a.GitHub.Key)
	if err != nil {
		return github.Token{}, refuse(http.StatusBadGateway, ReasonUpstream, "App key: "+err.Error())
	}
	tok, err := s.Minter.Mint(ctx, signer, github.Request{
		AppID: a.GitHub.AppID, InstallationID: a.GitHub.InstallationID,
		Repositories: []string{repo}, Permissions: a.GitHub.Permissions,
	})
	if err != nil {
		return github.Token{}, refuse(http.StatusBadGateway, ReasonUpstream, err.Error())
	}
	return tok, nil
}

// decide logs the decision and writes the answer: body on success, the
// refusal's reason code otherwise. Tokens are never logged.
func (s *Server) decide(w http.ResponseWriter, r *http.Request, action string, caller identity.ID, agent string, ref *refusal, body any) {
	attrs := []any{"action", action, "caller", caller.String(), "agent", agent}
	w.Header().Set("Content-Type", "application/json")
	if ref != nil {
		s.Log.InfoContext(r.Context(), "refused", append(attrs, "reason", ref.reason, "detail", ref.detail)...)
		w.WriteHeader(ref.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": false, "reason": ref.reason, "detail": ref.detail})
		return
	}
	s.Log.InfoContext(r.Context(), "allowed", attrs...)
	_ = json.NewEncoder(w).Encode(body)
}

// LocalSigners resolves "file:<path>" key references, caching each signer.
func LocalSigners() SignerFor {
	var mu sync.Mutex
	cache := map[string]github.Signer{}
	return func(key string) (github.Signer, error) {
		mu.Lock()
		defer mu.Unlock()
		if s, ok := cache[key]; ok {
			return s, nil
		}
		path, ok := strings.CutPrefix(key, "file:")
		if !ok {
			return nil, fmt.Errorf("unsupported key reference %q", key)
		}
		s, err := github.NewFileSigner(path)
		if err != nil {
			return nil, err
		}
		cache[key] = s
		return s, nil
	}
}
