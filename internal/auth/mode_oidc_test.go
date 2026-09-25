// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

const oidcTestAudience = "kafkito-api"

// buildOIDCMode starts a mock issuer and builds the "oidc" mode against it.
// jwksExplicit selects between a configured JWKS URL and discovery.
func buildOIDCMode(t *testing.T, jwksExplicit bool) (*auth.MockOIDC, auth.Validator) {
	t.Helper()
	mock, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	oc := auth.OIDCConfig{IssuerURL: mock.Server.URL, Audience: oidcTestAudience}
	if jwksExplicit {
		oc.JWKSEndpoint = mock.JKU()
	}
	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "oidc", OIDC: oc})
	require.NoError(t, err, "BuildValidator(oidc)")
	require.NotNil(t, cleanup)
	t.Cleanup(cleanup)
	require.NotNil(t, v)
	return mock, v
}

func TestOIDCMode_DiscoversJWKSURL(t *testing.T) {
	t.Parallel()

	mock, v := buildOIDCMode(t, false)

	ov, ok := v.(*auth.OIDCValidator)
	require.True(t, ok, "oidc mode must return *OIDCValidator, got %T", v)
	assert.Equal(t, mock.JKU(), ov.Config().JWKSEndpoint)
}

func TestOIDCMode_ValidatesTokens(t *testing.T) {
	t.Parallel()

	for _, explicit := range []bool{false, true} {
		name := "discovered_jwks"
		if explicit {
			name = "configured_jwks"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mock, v := buildOIDCMode(t, explicit)
			ctx := context.Background()

			good, err := mock.Issue("user-1", oidcTestAudience, mock.Server.URL, []string{"read"},
				map[string]any{"preferred_username": "alice"})
			require.NoError(t, err)
			p, err := v.Validate(ctx, good)
			require.NoError(t, err, "valid token must be accepted")
			assert.Equal(t, "user-1", p.Subject)
			assert.Equal(t, "alice", p.UserName)
			assert.True(t, p.HasScope("read"))

			wrongAud, err := mock.Issue("user-1", "someone-else", mock.Server.URL, nil, nil)
			require.NoError(t, err)
			_, err = v.Validate(ctx, wrongAud)
			assert.ErrorContains(t, err, "aud", "wrong audience must be rejected")

			wrongIss, err := mock.Issue("user-1", oidcTestAudience, "https://evil.example.com", nil, nil)
			require.NoError(t, err)
			_, err = v.Validate(ctx, wrongIss)
			assert.ErrorContains(t, err, "iss", "wrong issuer must be rejected")
		})
	}
}

func TestOIDCMode_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     auth.OIDCConfig
		wantErr string
	}{
		{"missing_issuer", auth.OIDCConfig{Audience: "a"}, "issuer"},
		{"missing_audience", auth.OIDCConfig{IssuerURL: "https://idp.example.com"}, "audience"},
		{"discovery_unreachable", auth.OIDCConfig{IssuerURL: "http://127.0.0.1:1", Audience: "a"}, "oidc discovery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "oidc", OIDC: tc.cfg})
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
