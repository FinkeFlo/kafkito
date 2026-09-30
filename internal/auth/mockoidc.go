// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// MockOIDC is an in-process JWKS+token issuer used in tests. It signs RS256
// tokens with a freshly generated key, exposes the public key at /jwks, and
// lets callers mint tokens with arbitrary claims via Issue().
//
// By default the mock emits a generic OIDC token: scopes are written to the
// "scope" claim verbatim, and no tenant/zone claim is added. Callers that need
// tenant-flavored tokens (e.g. SAP-style scope namespacing or a zone claim)
// can opt in via MockOIDCOption values passed to NewMockOIDC.
type MockOIDC struct {
	Server      *httptest.Server
	scopePrefix string
	zoneID      string
	jwksPath    string
	jwksHits    atomic.Int64

	mu     sync.RWMutex // guards the signing key, which RotateKey replaces
	priv   *rsa.PrivateKey
	keyID  string
	keySet jwk.Set
	keySeq int
}

// MockOIDCOption configures a MockOIDC at construction time.
type MockOIDCOption func(*MockOIDC)

// WithScopePrefix namespaces every scope passed to Issue/IssueWithoutJKU as
// prefix + "." + scope. An empty prefix disables namespacing (the default).
func WithScopePrefix(prefix string) MockOIDCOption {
	return func(m *MockOIDC) { m.scopePrefix = prefix }
}

// WithZoneID makes the mock emit a "zid" claim on every issued token. An empty
// zoneID disables emission (the default).
func WithZoneID(zoneID string) MockOIDCOption {
	return func(m *MockOIDC) { m.zoneID = zoneID }
}

// WithJWKSPath serves the key set at path instead of "/jwks". XSUAA tests use
// "/token_keys", the only path the xsuaa validator accepts in a jku.
func WithJWKSPath(path string) MockOIDCOption {
	return func(m *MockOIDC) { m.jwksPath = path }
}

// NewMockOIDC starts the mock and returns it. By default tokens carry no zone
// claim and scopes are not namespaced; use WithScopePrefix / WithZoneID to opt
// into tenant-flavored behavior.
func NewMockOIDC(opts ...MockOIDCOption) (*MockOIDC, error) {
	m := &MockOIDC{jwksPath: "/jwks"}
	for _, opt := range opts {
		opt(m)
	}
	if err := m.RotateKey(); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(m.jwksPath, func(w http.ResponseWriter, _ *http.Request) {
		m.jwksHits.Add(1)
		m.mu.RLock()
		set := m.keySet
		m.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	m.Server = httptest.NewServer(mux)
	return m, nil
}

// Close stops the test server.
func (m *MockOIDC) Close() { m.Server.Close() }

// JKU returns the JWKS URL the mock serves.
func (m *MockOIDC) JKU() string { return m.Server.URL + m.jwksPath }

// JWKSRequests returns how many requests the JWKS endpoint has answered.
func (m *MockOIDC) JWKSRequests() int64 { return m.jwksHits.Load() }

// RotateKey replaces the signing key with a new RSA key under a new kid
// ("test-key-1", "test-key-2", ...). The JWKS then serves only the new key,
// like an IdP that rotated and dropped the old one; tokens issued afterwards
// are signed with it. NewMockOIDC calls it once for the first key.
func (m *MockOIDC) RotateKey() error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	pub, err := jwk.Import(&priv.PublicKey)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	keyID := fmt.Sprintf("test-key-%d", m.keySeq+1)
	if err := pub.Set(jwk.KeyIDKey, keyID); err != nil {
		return err
	}
	if err := pub.Set(jwk.AlgorithmKey, jwa.RS256()); err != nil {
		return err
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		return err
	}
	m.priv, m.keyID, m.keySet = priv, keyID, set
	m.keySeq++
	return nil
}

// Host returns the bare hostname (no port) of the mock server, suitable for use
// as a UAADomain in tests.
func (m *MockOIDC) Host() string {
	u, _ := url.Parse(m.Server.URL)
	return u.Hostname()
}

// Issue mints a signed RS256 JWT with sensible defaults plus caller-supplied claims.
// Pass scopes as local names; if the mock was constructed with WithScopePrefix,
// each scope is namespaced as prefix + "." + scope.
func (m *MockOIDC) Issue(sub, clientID, issuer string, scopes []string, extra map[string]any) (string, error) {
	return m.sign(m.JKU(), m.claims(sub, clientID, issuer, scopes, extra))
}

// IssueWithJKU mints a token like Issue (without extra claims) but puts jku
// into the jku header, for testing the validator's jku policy.
func (m *MockOIDC) IssueWithJKU(jku, sub, clientID, issuer string, scopes []string) (string, error) {
	return m.sign(jku, m.claims(sub, clientID, issuer, scopes, nil))
}

// IssueWithoutJKU mints a token like Issue but deliberately omits the jku header,
// for testing the validator's "missing jku" rejection branch.
func (m *MockOIDC) IssueWithoutJKU(sub, clientID, issuer string, scopes []string) (string, error) {
	return m.sign("", m.claims(sub, clientID, issuer, scopes, nil))
}

// claims builds the default claim set plus extra (which may override defaults).
func (m *MockOIDC) claims(sub, clientID, issuer string, scopes []string, extra map[string]any) jwt.Token {
	tok := jwt.New()
	_ = tok.Set(jwt.SubjectKey, sub)
	_ = tok.Set(jwt.IssuerKey, issuer)
	_ = tok.Set(jwt.AudienceKey, []string{clientID})
	_ = tok.Set(jwt.IssuedAtKey, time.Now())
	_ = tok.Set(jwt.ExpirationKey, time.Now().Add(30*time.Minute))
	_ = tok.Set("cid", clientID)
	if m.zoneID != "" {
		_ = tok.Set("zid", m.zoneID)
	}
	_ = tok.Set("scope", m.namespacedScopes(scopes))
	for k, v := range extra {
		_ = tok.Set(k, v)
	}
	return tok
}

// sign signs tok with the current key. An empty jku leaves the header out.
func (m *MockOIDC) sign(jku string, tok jwt.Token) (string, error) {
	m.mu.RLock()
	priv, keyID := m.priv, m.keyID
	m.mu.RUnlock()

	hdr := jws.NewHeaders()
	_ = hdr.Set(jws.KeyIDKey, keyID)
	_ = hdr.Set(jws.AlgorithmKey, jwa.RS256())
	if jku != "" {
		_ = hdr.Set(jws.JWKSetURLKey, jku)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), priv, jws.WithProtectedHeaders(hdr)))
	if err != nil {
		return "", err
	}
	return string(signed), nil
}

// namespacedScopes applies the configured scope prefix (if any) to each scope.
func (m *MockOIDC) namespacedScopes(scopes []string) []string {
	if m.scopePrefix == "" {
		return append([]string(nil), scopes...)
	}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, m.scopePrefix+"."+s)
	}
	return out
}
