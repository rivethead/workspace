package github_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/example-org/workspace/broker/internal/github"
)

func keyFile(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "app.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, key
}

// fakeGitHub checks the App JWT with the App's public key, records the token
// request and answers like GitHub.
func fakeGitHub(t *testing.T, pub *rsa.PublicKey, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/202/access_tokens" || r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
		var claims jwt.Claims
		if err != nil || tok.Claims(pub, &claims) != nil || claims.Issuer != "101" {
			http.Error(w, `{"message":"A JSON web token could not be decoded"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_test","expires_at":"2030-01-01T00:00:00Z"}`))
	}))
}

func TestMintSignsWithTheAppKeyAndLimitsTheToken(t *testing.T) {
	path, key := keyFile(t)
	var got map[string]any
	srv := fakeGitHub(t, &key.PublicKey, &got)
	defer srv.Close()

	signer, err := github.NewFileSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	m := github.NewMinter()
	m.API = srv.URL
	tok, err := m.Mint(t.Context(), signer, github.Request{
		AppID: 101, InstallationID: 202,
		Repositories: []string{"example-org/workspace-sandbox"},
		Permissions:  map[string]string{"contents": "write"},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if tok.Token != "ghs_test" {
		t.Errorf("token = %q", tok.Token)
	}
	if repos := got["repositories"].([]any); len(repos) != 1 || repos[0] != "workspace-sandbox" {
		t.Errorf("repositories sent = %v, want [workspace-sandbox]", got["repositories"])
	}
	if perms := got["permissions"].(map[string]any); perms["contents"] != "write" || len(perms) != 1 {
		t.Errorf("permissions sent = %v", got["permissions"])
	}
}

func TestMintFailsWhenSignedByAnotherKey(t *testing.T) {
	path, _ := keyFile(t)
	_, other := keyFile(t)
	var got map[string]any
	srv := fakeGitHub(t, &other.PublicKey, &got)
	defer srv.Close()

	signer, _ := github.NewFileSigner(path)
	m := github.NewMinter()
	m.API = srv.URL
	_, err := m.Mint(t.Context(), signer, github.Request{AppID: 101, InstallationID: 202, Repositories: []string{"example-org/x"}})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want GitHub's 401", err)
	}
}

func TestAppJWTIsShortLived(t *testing.T) {
	path, key := keyFile(t)
	var exp time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, _ := jwt.ParseSigned(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), []jose.SignatureAlgorithm{jose.RS256})
		var c jwt.Claims
		_ = tok.Claims(&key.PublicKey, &c)
		exp = c.Expiry.Time().Sub(c.IssuedAt.Time())
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"t","expires_at":"2030-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	signer, _ := github.NewFileSigner(path)
	m := github.NewMinter()
	m.API = srv.URL
	if _, err := m.Mint(t.Context(), signer, github.Request{AppID: 101, InstallationID: 202, Repositories: []string{"o/r"}}); err != nil {
		t.Fatal(err)
	}
	if exp > 10*time.Minute {
		t.Fatalf("App JWT lives %v; GitHub allows at most 10 minutes", exp)
	}
}
