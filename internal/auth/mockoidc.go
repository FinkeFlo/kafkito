// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	priv        *rsa.PrivateKey
	pubJWK      jwk.Key
	keyID       string
	scopePrefix string
	zoneID      string
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

// NewMockOIDC starts the mock and returns it. By default tokens carry no zone
// claim and scopes are not namespaced; use WithScopePrefix / WithZoneID to opt
// into tenant-flavored behavior.
func NewMockOIDC(opts ...MockOIDCOption) (*MockOIDC, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	pub, err := jwk.Import(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	keyID := "test-key-1"
	if err := pub.Set(jwk.KeyIDKey, keyID); err != nil {
		return nil, err
	}
	if err := pub.Set(jwk.AlgorithmKey, jwa.RS256()); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		return nil, err
	}
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	srv := httptest.NewServer(mux)
	m := &MockOIDC{Server: srv, priv: priv, pubJWK: pub, keyID: keyID}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Close stops the test server.
func (m *MockOIDC) Close() { m.Server.Close() }

// JKU returns the JWKS URL the mock serves.
func (m *MockOIDC) JKU() string { return m.Server.URL + "/jwks" }

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

	hdr := jws.NewHeaders()
	_ = hdr.Set(jws.KeyIDKey, m.keyID)
	_ = hdr.Set(jws.AlgorithmKey, jwa.RS256())
	_ = hdr.Set("jku", m.JKU())

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), m.priv, jws.WithProtectedHeaders(hdr)))
	if err != nil {
		return "", err
	}
	return string(signed), nil
}

// IssueWithoutJKU mints a token like Issue but deliberately omits the jku header,
// for testing the validator's "missing jku" rejection branch.
func (m *MockOIDC) IssueWithoutJKU(sub, clientID, issuer string, scopes []string) (string, error) {
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
	hdr := jws.NewHeaders()
	_ = hdr.Set(jws.KeyIDKey, m.keyID)
	_ = hdr.Set(jws.AlgorithmKey, jwa.RS256())
	// intentionally NOT setting jku
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), m.priv, jws.WithProtectedHeaders(hdr)))
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
