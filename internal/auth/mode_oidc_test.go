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
// jwksExplicit selects between a configured JWKS URL and discovery; edit may
// adjust the config further.
func buildOIDCMode(t *testing.T, jwksExplicit bool, edit func(*auth.OIDCConfig)) (*auth.MockOIDC, auth.Validator) {
	t.Helper()
	mock, err := auth.NewMockOIDC()
	require.NoError(t, err, "NewMockOIDC")
	t.Cleanup(mock.Close)

	oc := auth.OIDCConfig{IssuerURL: mock.Server.URL, Audience: oidcTestAudience}
	if jwksExplicit {
		oc.JWKSEndpoint = mock.JKU()
	}
	if edit != nil {
		edit(&oc)
	}
	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "oidc", OIDC: oc})
	require.NoError(t, err, "BuildValidator(oidc)")
	require.NotNil(t, cleanup)
	t.Cleanup(cleanup)
	require.NotNil(t, v)
	return mock, v
}

func TestOIDCMode_DiscoversJWKSURLAndLoadsTheKeysAtStartup(t *testing.T) {
	t.Parallel()

	mock, v := buildOIDCMode(t, false, nil)

	ov, ok := v.(*auth.OIDCValidator)
	require.True(t, ok, "oidc mode must return *OIDCValidator, got %T", v)
	assert.Equal(t, mock.JKU(), ov.Config().JWKSEndpoint)
	assert.Equal(t, int64(1), mock.JWKSRequests(), "the keys must be loaded once at startup")
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
			mock, v := buildOIDCMode(t, explicit, nil)
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
			require.ErrorIs(t, err, auth.ErrAudienceMismatch, "wrong audience must be rejected")

			wrongIss, err := mock.Issue("user-1", oidcTestAudience, "https://evil.example.com", nil, nil)
			require.NoError(t, err)
			_, err = v.Validate(ctx, wrongIss)
			require.ErrorIs(t, err, auth.ErrIssuerMismatch, "wrong issuer must be rejected")

			subPath, err := mock.Issue("user-1", oidcTestAudience, mock.Server.URL+"/tenant", nil, nil)
			require.NoError(t, err)
			_, err = v.Validate(ctx, subPath)
			require.ErrorIs(t, err, auth.ErrIssuerMismatch, "an issuer sub-path must be rejected")

			noSub, err := mock.Token("", oidcTestAudience, mock.Server.URL).Without("sub").Sign()
			require.NoError(t, err)
			_, err = v.Validate(ctx, noSub)
			require.ErrorIs(t, err, auth.ErrSubMissing, "a token without sub must be rejected")
		})
	}
}

func TestOIDCMode_PassesTypAndAZPSettingsToTheValidator(t *testing.T) {
	t.Parallel()

	mock, v := buildOIDCMode(t, true, func(c *auth.OIDCConfig) {
		c.RequiredTyp = "at+jwt"
		c.AllowedAZP = []string{"proxy"}
	})
	ctx := context.Background()

	good, err := mock.Token("user-1", oidcTestAudience, mock.Server.URL).Type("at+jwt").Claim("azp", "proxy").Sign()
	require.NoError(t, err)
	_, err = v.Validate(ctx, good)
	require.NoError(t, err)

	idToken, err := mock.Token("user-1", oidcTestAudience, mock.Server.URL).Claim("azp", "proxy").Sign()
	require.NoError(t, err)
	_, err = v.Validate(ctx, idToken)
	require.ErrorIs(t, err, auth.ErrTypMismatch)

	otherClient, err := mock.Token("user-1", oidcTestAudience, mock.Server.URL).Type("at+jwt").Claim("azp", "other").Sign()
	require.NoError(t, err)
	_, err = v.Validate(ctx, otherClient)
	require.ErrorIs(t, err, auth.ErrAZPNotAllowed)
}

func TestOIDCMode_RejectsIncompleteOrUnsafeConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     auth.OIDCConfig
		wantErr string
	}{
		{"missing_issuer", auth.OIDCConfig{Audience: "a"}, "issuer"},
		{"missing_audience", auth.OIDCConfig{IssuerURL: "https://idp.example.com"}, "audience"},
		{"http_remote_issuer", auth.OIDCConfig{IssuerURL: "http://idp.example.com", Audience: "a"}, "https"},
		{"http_remote_jwks_url", auth.OIDCConfig{
			IssuerURL: "https://idp.example.com", Audience: "a", JWKSEndpoint: "http://idp.example.com/certs",
		}, "https"},
		{"issuer_with_query", auth.OIDCConfig{IssuerURL: "https://idp.example.com/?x=1", Audience: "a"}, "query or fragment"},
		{"discovery_unreachable", auth.OIDCConfig{IssuerURL: "http://127.0.0.1:1", Audience: "a"}, "oidc discovery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := auth.BuildValidator(auth.ModeConfig{Mode: "oidc", OIDC: tc.cfg})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// With an explicit JWKS URL there is no discovery, so an unreachable IdP only
// makes the warm-up fail: the mode still starts and its cleanup closes the
// key source.
func TestOIDCMode_WarmUpFailure_StillReturnsValidatorAndCleanup(t *testing.T) {
	t.Parallel()

	v, cleanup, err := auth.BuildValidator(auth.ModeConfig{Mode: "oidc", OIDC: auth.OIDCConfig{
		IssuerURL:    "https://idp.example.com",
		Audience:     "a",
		JWKSEndpoint: "http://127.0.0.1:1/certs",
	}})

	require.NoError(t, err, "a failed warm-up must not fail startup")
	require.NotNil(t, v)
	require.NotNil(t, cleanup)
	cleanup()
	_, err = v.Validate(context.Background(), "not.a.jwt")
	require.ErrorIs(t, err, auth.ErrKeySourceClosed, "cleanup must close the validator")
}
