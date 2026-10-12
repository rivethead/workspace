// Package identity checks the JWT-SVID a caller presents and returns its SPIFFE ID.
//
// Every service behind agentgateway checks the token itself, with its own audience,
// so no component trusts a forwarded header (ARCHITECTURE.md D42).
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// ID is a workload's SPIFFE ID in our trust domain: spiffe://<td>/<kind>/<name>.
// Kinds in use: "agent" (an agent's run) and "launcher" (a laptop's launcher).
type ID struct {
	Kind string
	Name string
}

func (id ID) String() string { return id.Kind + "/" + id.Name }

// ParseID parses a SPIFFE ID of the form spiffe://<trustDomain>/<kind>/<name>.
func ParseID(raw, trustDomain string) (ID, error) {
	rest, ok := strings.CutPrefix(raw, "spiffe://"+trustDomain+"/")
	if !ok {
		return ID{}, fmt.Errorf("not a SPIFFE ID in trust domain %s: %q", trustDomain, raw)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ID{}, fmt.Errorf("SPIFFE ID must be spiffe://%s/<kind>/<name>: %q", trustDomain, raw)
	}
	return ID{Kind: parts[0], Name: parts[1]}, nil
}

// Verifier checks JWT-SVIDs against the key set SPIRE publishes (the OIDC
// discovery provider's /keys). Keys are cached and fetched again when a token
// names a key that is not in the cache, so SPIRE's key rotation needs no
// restart. Fetches happen one at a time, and at most once per MinRefetch after
// one succeeds, so tokens with made-up key IDs cannot slow down checks of real
// ones.
type Verifier struct {
	jwksURL     string
	trustDomain string
	audience    string
	client      *http.Client
	MinRefetch  time.Duration

	fetch     sync.Mutex // held while fetching
	mu        sync.Mutex // guards keys and lastFetch
	keys      jose.JSONWebKeySet
	lastFetch time.Time
}

func NewVerifier(jwksURL, trustDomain, audience string) *Verifier {
	return &Verifier{jwksURL: jwksURL, trustDomain: trustDomain, audience: audience, client: &http.Client{Timeout: 5 * time.Second}, MinRefetch: 30 * time.Second}
}

// Verify checks the signature, expiry and audience of raw, and returns the
// caller's SPIFFE ID.
func (v *Verifier) Verify(ctx context.Context, raw string) (ID, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256, jose.ES256, jose.ES384, jose.PS256})
	if err != nil {
		return ID{}, fmt.Errorf("parse token: %w", err)
	}
	if len(tok.Headers) != 1 {
		return ID{}, errors.New("token must have exactly one signature")
	}
	key, err := v.key(ctx, tok.Headers[0].KeyID)
	if err != nil {
		return ID{}, err
	}
	var claims jwt.Claims
	if err := tok.Claims(key, &claims); err != nil {
		return ID{}, fmt.Errorf("token signature: %w", err)
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{AnyAudience: jwt.Audience{v.audience}, Time: time.Now()}, 30*time.Second); err != nil {
		return ID{}, fmt.Errorf("token claims: %w", err)
	}
	return ParseID(claims.Subject, v.trustDomain)
}

func (v *Verifier) key(ctx context.Context, kid string) (*jose.JSONWebKey, error) {
	if k, ok := v.cached(kid); ok {
		return k, nil
	}
	v.fetch.Lock()
	defer v.fetch.Unlock()
	if k, ok := v.cached(kid); ok { // fetched while this call waited
		return k, nil
	}
	v.mu.Lock()
	recent := !v.lastFetch.IsZero() && time.Since(v.lastFetch) < v.MinRefetch
	v.mu.Unlock()
	if !recent {
		set, err := v.download(ctx)
		if err != nil {
			return nil, err
		}
		v.mu.Lock()
		v.keys, v.lastFetch = set, time.Now()
		v.mu.Unlock()
	}
	if k, ok := v.cached(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("no key %q in %s", kid, v.jwksURL)
}

func (v *Verifier) cached(kid string) (*jose.JSONWebKey, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k := v.keys.Key(kid); len(k) > 0 {
		return &k[0], true
	}
	return nil, false
}

func (v *Verifier) download(ctx context.Context) (jose.JSONWebKeySet, error) {
	var set jose.JSONWebKeySet
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return set, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return set, fmt.Errorf("fetch keys from %s: %w", v.jwksURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return set, fmt.Errorf("fetch keys from %s: %s", v.jwksURL, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return set, fmt.Errorf("decode keys from %s: %w", v.jwksURL, err)
	}
	return set, nil
}
