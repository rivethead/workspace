package identity_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/example-org/workspace/broker/internal/identity"
)

type issuer struct {
	key     *rsa.PrivateKey
	srv     *httptest.Server
	fetches atomic.Int32
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
	i := &issuer{key: key}
	i.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i.fetches.Add(1)
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(i.srv.Close)
	return i
}

func (i *issuer) token(t *testing.T, sub string, aud []string, exp time.Time) string {
	t.Helper()
	return i.tokenWithKey(t, "k1", sub, aud, exp)
}

func (i *issuer) tokenWithKey(t *testing.T, kid, sub string, aud []string, exp time.Time) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: i.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(jwt.Claims{Subject: sub, Audience: aud, Expiry: jwt.NewNumericDate(exp)}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newVerifier(i *issuer) *identity.Verifier {
	return identity.NewVerifier(i.srv.URL, "workspace.local", "broker")
}

func TestVerifyReturnsTheCallersSPIFFEID(t *testing.T) {
	i := newIssuer(t)
	tok := i.token(t, "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))

	id, err := newVerifier(i).Verify(t.Context(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Kind != "agent" || id.Name != "ada" {
		t.Fatalf("got %+v, want agent ada", id)
	}
}

func TestVerifyRefusesATokenForAnotherService(t *testing.T) {
	i := newIssuer(t)
	tok := i.token(t, "spiffe://workspace.local/agent/ada", []string{"assignments"}, time.Now().Add(time.Minute))

	if _, err := newVerifier(i).Verify(t.Context(), tok); err == nil {
		t.Fatal("a token for the assignments audience was accepted by the broker")
	}
}

func TestVerifyRefusesAnExpiredToken(t *testing.T) {
	i := newIssuer(t)
	tok := i.token(t, "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(-2*time.Minute))

	if _, err := newVerifier(i).Verify(t.Context(), tok); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestVerifyRefusesAnotherTrustDomain(t *testing.T) {
	i := newIssuer(t)
	tok := i.token(t, "spiffe://elsewhere.example/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))

	if _, err := newVerifier(i).Verify(t.Context(), tok); err == nil {
		t.Fatal("a SPIFFE ID from another trust domain was accepted")
	}
}

func TestVerifyRefusesATokenSignedByAnotherKey(t *testing.T) {
	i, other := newIssuer(t), newIssuer(t)
	tok := other.token(t, "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))

	if _, err := newVerifier(i).Verify(t.Context(), tok); err == nil {
		t.Fatal("a token signed by an unknown key was accepted")
	}
}

func TestParseID(t *testing.T) {
	cases := map[string]struct {
		kind, name string
		ok         bool
	}{
		"spiffe://workspace.local/agent/ada":         {"agent", "ada", true},
		"spiffe://workspace.local/launcher/alex.mac": {"launcher", "alex.mac", true},
		"spiffe://workspace.local/agent/ada/extra":   {"", "", false},
		"spiffe://workspace.local/":                  {"", "", false},
		"https://workspace.local/agent/ada":          {"", "", false},
	}
	for raw, want := range cases {
		id, err := identity.ParseID(raw, "workspace.local")
		if (err == nil) != want.ok || id.Kind != want.kind || id.Name != want.name {
			t.Errorf("ParseID(%q) = %+v, %v; want %s/%s ok=%v", raw, id, err, want.kind, want.name, want.ok)
		}
	}
}

func TestUnknownKeyIDsDoNotFetchTheKeysOnEveryRequest(t *testing.T) {
	i := newIssuer(t)
	v := newVerifier(i)
	if _, err := v.Verify(t.Context(), i.token(t, "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		tok := i.tokenWithKey(t, "made-up", "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))
		if _, err := v.Verify(t.Context(), tok); err == nil {
			t.Fatal("a token with an unknown key ID was accepted")
		}
	}
	if got := i.fetches.Load(); got != 1 {
		t.Fatalf("keys fetched %d times, want 1", got)
	}
}

func TestANewKeyIsFetchedOnceTheWaitIsOver(t *testing.T) {
	i := newIssuer(t)
	v := newVerifier(i)
	v.MinRefetch = 0
	for range 2 {
		tok := i.tokenWithKey(t, "rotated", "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))
		_, _ = v.Verify(t.Context(), tok)
	}
	if got := i.fetches.Load(); got != 2 {
		t.Fatalf("keys fetched %d times, want 2 (once per unknown key once the wait is over)", got)
	}
}

func TestAFailedKeyFetchIsRetriedOnTheNextRequest(t *testing.T) {
	i := newIssuer(t)
	up := false
	i.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
		_ = json.NewEncoder(w).Encode(set)
	})
	v := newVerifier(i)
	tok := i.token(t, "spiffe://workspace.local/agent/ada", []string{"broker"}, time.Now().Add(time.Minute))
	if _, err := v.Verify(t.Context(), tok); err == nil {
		t.Fatal("a token was accepted without keys")
	}
	up = true
	if _, err := v.Verify(t.Context(), tok); err != nil {
		t.Fatalf("the key server came back, but the token is still refused: %v", err)
	}
}
