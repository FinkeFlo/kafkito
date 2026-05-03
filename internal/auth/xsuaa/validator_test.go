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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

func newValidator(t *testing.T) (*auth.MockOIDC, auth.Validator, xsuaa.Credentials) {
	t.Helper()

	mock, err := auth.NewMockOIDC(
		auth.WithScopePrefix("kafkito!t12345"),
		auth.WithZoneID("test-zone"),
	)
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	creds := xsuaa.Credentials{
		ClientID:       "sb-kafkito!t12345",
		URL:            mock.Server.URL,
		UAADomain:      mock.Host(), // bare hostname (no port) for the UAADomain allow-list
		XSAppName:      "kafkito!t12345",
		IdentityZoneID: "test-zone",
	}
	v, err := xsuaa.NewValidator(creds)
	require.NoError(t, err, "NewValidator")

	return mock, v, creds
}

func TestValidator_HappyPath_AcceptsCanonicalToken(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	tok, err := mock.Issue("u-1", creds.ClientID, creds.URL, []string{"Display"}, nil)
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	require.NotNil(t, p)
	assert.Equal(t, "u-1", p.Subject)
	assert.True(t, p.HasScope("Display"), "HasScope(Display); scopes=%v", p.Scopes)
}

func TestValidator_HappyPath_AcceptsAudienceViaXSAppName(t *testing.T) {
	t.Parallel()

	mock, v, creds := newValidator(t)
	// Issue with aud = xsappname (no sb- prefix), simulating an XSUAA flow that targets the app directly.
	tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
		"aud": []string{creds.XSAppName},
	})
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	require.NotNil(t, p)
	assert.Equal(t, "u", p.Subject)
}

func TestValidator_RejectsInvalidToken(t *testing.T) {
	t.Parallel()

	// mutateCreds: optional callback to alter the Credentials before building the
	// validator. Used by rows that need a non-default validator (e.g. wrong UAADomain).
	// issueToken: produces the JWT (or post-Issue tampered string) under test for the row.
	type tokenIssuer func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string

	cases := []struct {
		name             string
		mutateCreds      func(*xsuaa.Credentials)
		issueToken       tokenIssuer
		wantErrSubstring string
	}{
		{
			name: "wrong_issuer",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, "https://evil.example", []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "iss",
		},
		{
			// Upstream library wording for audience errors isn't part of our public
			// contract, so we only require that an error is returned.
			name: "wrong_audience",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", "sb-other!t12345", creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
		},
		{
			name: "bad_jku_host",
			mutateCreds: func(c *xsuaa.Credentials) {
				c.UAADomain = "different.example.com"
			},
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				// creds passed here already carries the original UAADomain; the
				// mutated copy is used only for validator construction.
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "jku",
		},
		{
			name: "missing_jku",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				raw, err := mock.IssueWithoutJKU("u-1", creds.ClientID, creds.URL, []string{"Display"})
				require.NoError(t, err, "IssueWithoutJKU")
				return raw
			},
			wantErrSubstring: "jku",
		},
		{
			// Signature error wording isn't substring-stable across upstream versions.
			name: "tampered_signature",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")

				// JWS compact form is "<header>.<payload>.<signature>". Mutate one
				// byte in the decoded signature so the signature is guaranteed
				// different from any valid one.
				parts := strings.Split(tok, ".")
				require.Len(t, parts, 3, "expected 3 JWS parts")
				sig, err := base64.RawURLEncoding.DecodeString(parts[2])
				require.NoError(t, err, "decode signature")
				require.GreaterOrEqual(t, len(sig), 8, "signature suspiciously short")
				sig[len(sig)/2] ^= 0xFF // flip every bit of one middle byte
				parts[2] = base64.RawURLEncoding.EncodeToString(sig)
				return strings.Join(parts, ".")
			},
		},
		{
			name: "expired_token",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				past := time.Now().Add(-5 * time.Minute).Unix()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
					"exp": past,
					"iat": time.Now().Add(-10 * time.Minute).Unix(),
				})
				require.NoError(t, err, "Issue")
				return tok
			},
		},
		{
			name: "wrong_zid",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, map[string]any{
					"zid": "different-zone",
				})
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "zid",
		},
		{
			// UAADomain="ost" must not match mock.Host() == "127.0.0.1" by suffix.
			name: "hostname_substring_match",
			mutateCreds: func(c *xsuaa.Credentials) {
				c.UAADomain = "ost"
			},
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				tok, err := mock.Issue("u", creds.ClientID, creds.URL, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "jku",
		},
		{
			// iss = creds.URL + ".evil.com" — strings.HasPrefix would match without the / boundary check.
			name: "issuer_suffix_attack",
			issueToken: func(t *testing.T, mock *auth.MockOIDC, creds xsuaa.Credentials) string {
				t.Helper()
				evilIss := creds.URL + ".evil.com"
				tok, err := mock.Issue("u", creds.ClientID, evilIss, []string{"Display"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "iss",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, creds := newValidator(t)
			if tc.mutateCreds != nil {
				mutated := creds
				tc.mutateCreds(&mutated)
				var err error
				v, err = xsuaa.NewValidator(mutated)
				require.NoError(t, err, "NewValidator(mutated)")
			}
			tok := tc.issueToken(t, mock, creds)

			_, err := v.Validate(context.Background(), tok)

			require.Error(t, err, "Validate must reject")
			if tc.wantErrSubstring != "" {
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			}
		})
	}
}

func TestNewValidator_RejectsUAADomainWithPort(t *testing.T) {
	t.Parallel()

	_, err := xsuaa.NewValidator(xsuaa.Credentials{
		ClientID: "x", URL: "https://x.example", UAADomain: "x.example:8080", XSAppName: "x",
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "UAADomain")
}
