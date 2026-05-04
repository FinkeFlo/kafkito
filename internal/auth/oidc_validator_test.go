// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// alg=none JWT with sub=attacker — must be rejected by Validate. Public-test
// payload, no real secret material. Gitleaks flags the high-entropy literal.
// header: {"alg":"none","typ":"JWT"} -> base64url
// payload: {"sub":"attacker","aud":"test-audience"} -> base64url
const algNoneToken = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + // gitleaks:allow
	"eyJzdWIiOiJhdHRhY2tlciIsImF1ZCI6InRlc3QtYXVkaWVuY2UifQ." // gitleaks:allow

// newOIDCValidator returns a generic mock issuer (no scope prefix, no zid)
// wired up to a freshly constructed OIDCValidator. The audience is fixed to
// "test-audience" so callers can mint matching/mismatching tokens.
func newOIDCValidator(t *testing.T) (*auth.MockOIDC, auth.Validator, string) {
	t.Helper()

	mock, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	const audience = "test-audience"
	v, err := auth.NewOIDCValidator(auth.OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     audience,
		JWKSEndpoint: mock.JKU(),
	})
	require.NoError(t, err, "NewOIDCValidator")

	return mock, v, audience
}

func TestOIDCValidator_HappyPath_AcceptsScopeStringClaim(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tok, err := mock.Issue("user-123", aud, mock.Server.URL, []string{"read", "write"}, nil)
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.Equal(t, "user-123", p.Subject)
	assert.True(t, p.HasScope("read"), "HasScope(read); got scopes=%v", p.Scopes)
	assert.True(t, p.HasScope("write"), "HasScope(write); got scopes=%v", p.Scopes)
}

func TestOIDCValidator_HappyPath_AcceptsScopesArrayClaim(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	// Issue without scope claim, then layer "scopes" array via extra. We also
	// blank out the default "scope" claim so only "scopes" carries the data.
	tok, err := mock.Issue("user-arr", aud, mock.Server.URL, nil, map[string]any{
		"scopes": []string{"admin", "viewer"},
	})
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate")
	assert.True(t, p.HasScope("admin"), "HasScope(admin); got scopes=%v", p.Scopes)
	assert.True(t, p.HasScope("viewer"), "HasScope(viewer); got scopes=%v", p.Scopes)
}

func TestNewOIDCValidator_RejectsMissingConfigFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		cfg           auth.OIDCConfig
		wantErrSubstr string
	}{
		{
			name:          "missing_issuer_url",
			cfg:           auth.OIDCConfig{Audience: "a", JWKSEndpoint: "https://x/jwks"},
			wantErrSubstr: "IssuerURL required",
		},
		{
			name:          "missing_audience",
			cfg:           auth.OIDCConfig{IssuerURL: "https://x", JWKSEndpoint: "https://x/jwks"},
			wantErrSubstr: "Audience required",
		},
		{
			name:          "missing_jwks_endpoint",
			cfg:           auth.OIDCConfig{IssuerURL: "https://x", Audience: "a"},
			wantErrSubstr: "JWKSEndpoint required",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := auth.NewOIDCValidator(tc.cfg)

			require.Error(t, err, "NewOIDCValidator must reject %s", tc.name)
			assert.ErrorContains(t, err, tc.wantErrSubstr,
				"error must name the missing field so operators can fix the misconfiguration")
		})
	}
}

func TestOIDCValidator_AcceptsMultiTenantIssuerPrefix(t *testing.T) {
	t.Parallel()

	mock, v, aud := newOIDCValidator(t)
	tenantIss := mock.Server.URL + "/tenant-42"
	tok, err := mock.Issue("user-mt", aud, tenantIss, []string{"read"}, nil)
	require.NoError(t, err, "Issue")

	p, err := v.Validate(context.Background(), tok)

	require.NoError(t, err, "Validate must accept iss that sits under the configured IssuerURL")
	assert.Equal(t, "user-mt", p.Subject)
}

func TestOIDCValidator_RejectsInvalidToken(t *testing.T) {
	t.Parallel()

	// tokenForRow builds the token for a row. Returning nil means the row
	// validates a literal raw string (rawToken) instead of a freshly issued
	// JWT — used for malformed and alg=none cases.
	type tokenIssuer func(t *testing.T, mock *auth.MockOIDC, aud string) string

	cases := []struct {
		name             string
		issue            tokenIssuer
		rawToken         string
		wantErrSubstring string
	}{
		{
			name: "wrong_audience",
			issue: func(t *testing.T, mock *auth.MockOIDC, _ string) string {
				t.Helper()
				tok, err := mock.Issue("u", "other-audience", mock.Server.URL, []string{"read"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "aud",
		},
		{
			name: "wrong_issuer",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				tok, err := mock.Issue("u", aud, "https://evil.example", []string{"read"}, nil)
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "iss",
		},
		{
			// Distinct from wrong_audience: the aud claim is empty, not
			// merely mismatched. Locks oidc_validator.go:97 (len(auds)==0)
			// separately from oidc_validator.go:99-100 (AudienceContains
			// false).
			name: "aud_claim_missing",
			issue: func(t *testing.T, mock *auth.MockOIDC, _ string) string {
				t.Helper()
				tok, err := mock.Issue("u", "ignored", mock.Server.URL,
					[]string{"read"}, map[string]any{"aud": []string{}})
				require.NoError(t, err, "Issue")
				return tok
			},
			wantErrSubstring: "aud claim missing",
		},
		{
			// Substring not asserted: the upstream library's wording for
			// expiry isn't part of our public contract. Just require an error.
			name: "expired_token",
			issue: func(t *testing.T, mock *auth.MockOIDC, aud string) string {
				t.Helper()
				past := time.Now().Add(-10 * time.Minute).Unix()
				tok, err := mock.Issue("u", aud, mock.Server.URL, []string{"read"}, map[string]any{
					"exp": past,
					"iat": time.Now().Add(-20 * time.Minute).Unix(),
				})
				require.NoError(t, err, "Issue")
				return tok
			},
		},
		{
			name:     "malformed_token_garbage",
			rawToken: "not.a.jwt",
		},
		{
			name:     "malformed_token_empty",
			rawToken: "",
		},
		{
			name:     "alg_none_unsigned",
			rawToken: algNoneToken,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock, v, aud := newOIDCValidator(t)
			tok := tc.rawToken
			if tc.issue != nil {
				tok = tc.issue(t, mock, aud)
			}

			_, err := v.Validate(context.Background(), tok)

			require.Error(t, err, "Validate must reject this token")
			if tc.wantErrSubstring != "" {
				assert.ErrorContains(t, err, tc.wantErrSubstring)
			}
		})
	}
}
