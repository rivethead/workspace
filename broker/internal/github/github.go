// Package github mints GitHub tokens for an agent with the agent's own GitHub
// App. The App's private key never leaves its Signer: in the cloud that is KMS
// (kms:Sign); locally, a key file.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Signer signs the SHA-256 digest of a GitHub App JWT with RSASSA-PKCS1-v1_5
// (RS256), the only algorithm GitHub accepts for App JWTs.
type Signer interface {
	SignRS256(ctx context.Context, digest []byte) ([]byte, error)
}

// FileSigner signs with an RSA key in a PEM file. Local use only; in the cloud the
// key is imported into KMS and this is replaced by a KMS signer.
type FileSigner struct{ key *rsa.PrivateKey }

func NewFileSigner(path string) (*FileSigner, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read App key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("App key %s is not PEM", path)
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var k any
		k, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if rk, ok := k.(*rsa.PrivateKey); ok {
			key = rk
		} else if err == nil {
			err = errors.New("not an RSA key")
		}
	default:
		err = fmt.Errorf("unexpected PEM block %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("parse App key %s: %w", path, err)
	}
	return &FileSigner{key: key}, nil
}

func (f *FileSigner) SignRS256(_ context.Context, digest []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest)
}

// Token is a GitHub installation token.
type Token struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Request is what one token may cover: an installation, the repositories and
// the permissions. GitHub enforces all three.
type Request struct {
	AppID          int64
	InstallationID int64
	Repositories   []string // "owner/name"
	Permissions    map[string]string
}

type Minter struct {
	API    string // https://api.github.com
	Client *http.Client
	Now    func() time.Time
}

func NewMinter() *Minter {
	return &Minter{API: "https://api.github.com", Client: &http.Client{Timeout: 10 * time.Second}, Now: time.Now}
}

// Mint signs an App JWT with signer and exchanges it for an installation
// token limited to req's repositories and permissions (valid for one hour).
func (m *Minter) Mint(ctx context.Context, signer Signer, req Request) (Token, error) {
	appJWT, err := m.appJWT(ctx, signer, req.AppID)
	if err != nil {
		return Token{}, err
	}
	names := make([]string, 0, len(req.Repositories))
	for _, full := range req.Repositories {
		_, name, ok := strings.Cut(full, "/")
		if !ok {
			return Token{}, fmt.Errorf("repository must be owner/name: %q", full)
		}
		names = append(names, name)
	}
	body, _ := json.Marshal(map[string]any{"repositories": names, "permissions": req.Permissions})
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", m.API, req.InstallationID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+appJWT)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := m.Client.Do(httpReq)
	if err != nil {
		return Token{}, fmt.Errorf("request installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return Token{}, fmt.Errorf("GitHub refused an installation token for installation %d: %s %s", req.InstallationID, resp.Status, e.Message)
	}
	var tok Token
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return Token{}, fmt.Errorf("decode installation token: %w", err)
	}
	return tok, nil
}

// appJWT builds the short JWT that authenticates as the App: issued a minute in
// the past for clock drift, valid for nine minutes (GitHub allows ten).
func (m *Minter) appJWT(ctx context.Context, signer Signer, appID int64) (string, error) {
	now := m.Now()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	})
	signing := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := signer.SignRS256(ctx, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign App JWT for App %d: %w", appID, err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
