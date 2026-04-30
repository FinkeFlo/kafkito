//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

func newValidator(t *testing.T) (*auth.MockOIDC, auth.Validator, xsuaa.Credentials) {
	t.Helper()
	mock, err := auth.NewMockOIDC(
		auth.WithScopePrefix("kafkito!t12345"),
		auth.WithZoneID("test-zone"),
	)
	if err != nil {
		t.Fatalf("mock: %v", err)
	}
	t.Cleanup(mock.Close)
	creds := xsuaa.Credentials{
		ClientID:       "sb-kafkito!t12345",
		URL:            mock.Server.URL,
		UAADomain:      mock.Host(), // bare hostname (no port) for the UAADomain allow-list
		XSAppName:      "kafkito!t12345",
		IdentityZoneID: "test-zone",
	}
	v, err := xsuaa.NewValidator(creds)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return mock, v, creds
}

func TestValidator_HappyPath(t *testing.T) {
	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u-1", creds.ClientID, creds.URL, []string{"Display"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	p, err := v.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.Subject != "u-1" {
		t.Errorf("Subject = %q", p.Subject)
	}
	if !p.HasScope("Display") {
		t.Errorf("expected Display scope, got %v", p.Scopes)
	}
}

func TestValidator_RejectsWrongIssuer(t *testing.T) {
	mock, v, creds := newValidator(t)
	tok, _ := mock.Issue("u", creds.ClientID, "https://evil.example", []string{"Display"}, nil)
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "iss") {
		t.Errorf("want issuer error, got %v", err)
	}
}

func TestValidator_RejectsWrongAudience(t *testing.T) {
	mock, v, creds := newValidator(t)
	tok, _ := mock.Issue("u", "sb-other!t12345", creds.URL, []string{"Display"}, nil)
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Errorf("want audience error")
	}
}

func TestValidator_RejectsBadJKUHost(t *testing.T) {
	mock, _, creds := newValidator(t)
	creds.UAADomain = "different.example.com"
	v, _ := xsuaa.NewValidator(creds)
	tok, _ := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "jku") {
		t.Errorf("want jku error, got %v", err)
	}
}

func TestValidator_RejectsMissingJKU(t *testing.T) {
	mock, v, creds := newValidator(t)
	raw, err := mock.IssueWithoutJKU("u-1", creds.ClientID, creds.URL, []string{"Display"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Validate(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "jku") {
		t.Errorf("want jku error, got %v", err)
	}
}

func TestValidator_RejectsTamperedSignature(t *testing.T) {
	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// JWS compact form is "<header>.<payload>.<signature>". Mutate one byte
	// in the decoded signature so the signature is guaranteed different.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWS parts, got %d", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) < 8 {
		t.Fatalf("signature suspiciously short: %d bytes", len(sig))
	}
	sig[len(sig)/2] ^= 0xFF // flip every bit of one middle byte
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	tampered := strings.Join(parts, ".")

	if _, err := v.Validate(context.Background(), tampered); err == nil {
		t.Errorf("want signature error, got nil")
	}
}

func TestValidator_RejectsExpiredToken(t *testing.T) {
	mock, v, creds := newValidator(t)
	past := time.Now().Add(-5 * time.Minute).Unix()
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
		"exp": past,
		"iat": time.Now().Add(-10 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Validate(context.Background(), tok); err == nil {
		t.Errorf("want expiration error")
	}
}

func TestValidator_RejectsWrongZID(t *testing.T) {
	mock, v, creds := newValidator(t)
	_ = creds
	tok, _ := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
		"zid": "different-zone",
	})
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "zid") {
		t.Errorf("want zid error, got %v", err)
	}
}

func TestValidator_AcceptsAudienceViaXSAppName(t *testing.T) {
	mock, v, creds := newValidator(t)
	// Issue with aud = xsappname (no sb- prefix), simulating an XSUAA flow that targets the app directly.
	tok, _ := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
		"aud": []string{creds.XSAppName},
	})
	p, err := v.Validate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.Subject != "u" {
		t.Errorf("Subject = %q", p.Subject)
	}
}

func TestValidator_RejectsHostnameSubstringMatch(t *testing.T) {
	mock, _, creds := newValidator(t)
	// UAADomain set to something whose suffix-match with the mock host would be wrong.
	creds.UAADomain = "ost" // mock.Host() == "127.0.0.1" — does not end with ".ost" and is not "ost"
	v, _ := xsuaa.NewValidator(creds)
	tok, _ := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "jku") {
		t.Errorf("want jku error, got %v", err)
	}
}

func TestNewValidator_RejectsUAADomainWithPort(t *testing.T) {
	_, err := xsuaa.NewValidator(xsuaa.Credentials{
		ClientID: "x", URL: "https://x.example", UAADomain: "x.example:8080", XSAppName: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "UAADomain") {
		t.Errorf("want UAADomain validation error, got %v", err)
	}
}

func TestValidator_RejectsIssuerSuffixAttack(t *testing.T) {
	mock, v, creds := newValidator(t)
	// Issue with iss = creds.URL + ".evil.com" — strings.HasPrefix would match without the / boundary.
	evilIss := creds.URL + ".evil.com"
	tok, _ := mock.Issue("u", creds.ClientID, evilIss, []string{"Display"}, nil)
	if _, err := v.Validate(context.Background(), tok); err == nil || !strings.Contains(err.Error(), "iss") {
		t.Errorf("want iss error, got %v", err)
	}
}
