// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// newOIDCValidator returns a generic mock issuer (no scope prefix, no zid)
// wired up to a freshly constructed OIDCValidator. The audience is fixed to
// "test-audience" so callers can mint matching/mismatching tokens.
func newOIDCValidator(t *testing.T) (*auth.MockOIDC, auth.Validator, string) {
	t.Helper()
	mock, err := auth.NewMockOIDC()
	if err != nil {
		t.Fatalf("mock: %v", err)
	}
	t.Cleanup(mock.Close)
	const audience = "test-audience"
	v, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     audience,
		JWKSEndpoint: mock.JKU(),
	})
	if err != nil {
		t.Fatalf("NewOIDCValidator: %v", err)
	}
	return mock, v, audience
}

func TestOIDCValidator_HappyPath_ScopeString(t *testing.T) {
	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("user-123", aud, mock.Server.URL, []string{"read", "write"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	p, err := v.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.Subject != "user-123" {
		t.Errorf("Subject = %q, want %q", p.Subject, "user-123")
	}
	if !p.HasScope("read") || !p.HasScope("write") {
		t.Errorf("expected read+write scopes, got %v", p.Scopes)
	}
}

func TestOIDCValidator_HappyPath_ScopesArray(t *testing.T) {
	mock, v, aud := newOIDCValidator(t)
	// Issue without scope claim, then layer "scopes" array via extra. We also
	// blank out the default "scope" claim so only "scopes" carries the data.
	tok, err := mock.Issue("user-arr", aud, mock.Server.URL, nil, map[string]any{
		"scopes": []string{"admin", "viewer"},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	p, err := v.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !p.HasScope("admin") || !p.HasScope("viewer") {
		t.Errorf("expected admin+viewer scopes, got %v", p.Scopes)
	}
}

func TestOIDCValidator_RejectsWrongAudience(t *testing.T) {
	mock, v, _ := newOIDCValidator(t)
	tok, err := mock.Issue("u", "other-audience", mock.Server.URL, []string{"read"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "aud") {
		t.Errorf("want audience error, got %v", err)
	}
}

func TestOIDCValidator_RejectsWrongIssuer(t *testing.T) {
	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("u", aud, "https://evil.example", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "iss") {
		t.Errorf("want issuer error, got %v", err)
	}
}

func TestOIDCValidator_RejectsExpiredToken(t *testing.T) {
	mock, v, aud := newOIDCValidator(t)
	past := time.Now().Add(-10 * time.Minute).Unix()
	tok, err := mock.Issue("u", aud, mock.Server.URL, []string{"read"}, map[string]any{
		"exp": past,
		"iat": time.Now().Add(-20 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Errorf("want expiration error, got nil")
	}
}

func TestOIDCValidator_RejectsMalformedToken(t *testing.T) {
	_, v, _ := newOIDCValidator(t)
	if _, err := v.Validate(context.Background(), "not.a.jwt"); err == nil {
		t.Errorf("want parse error for malformed token, got nil")
	}
	if _, err := v.Validate(context.Background(), ""); err == nil {
		t.Errorf("want error for empty token, got nil")
	}
}

func TestOIDCValidator_RejectsUnsignedToken(t *testing.T) {
	_, v, _ := newOIDCValidator(t)
	// alg=none JWT with sub=attacker — must be rejected.
	// header: {"alg":"none","typ":"JWT"} -> base64url
	// payload: {"sub":"attacker","aud":"test-audience"} -> base64url
	const unsigned = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiJhdHRhY2tlciIsImF1ZCI6InRlc3QtYXVkaWVuY2UifQ."
	if _, err := v.Validate(context.Background(), unsigned); err == nil {
		t.Errorf("want error for alg=none token, got nil")
	}
}
