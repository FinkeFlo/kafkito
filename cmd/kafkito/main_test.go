// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
	"github.com/FinkeFlo/kafkito/internal/config"
)

// run mutates the default slog logger; restore it for the rest of the package.
func restoreDefaultLogger(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestRun_ReturnsExitCode2_WhenConfigFileIsMissing(t *testing.T) {
	restoreDefaultLogger(t)

	assert.Equal(t, 2, run(filepath.Join(t.TempDir(), "missing.yaml")))
}

// Mode "off" is refused either at auth init (builds without it) or by the
// loopback guard (non-loopback bind, no acknowledgement); both exit with 2.
func TestRun_ReturnsExitCode2_WhenAuthModeOffIsRefused(t *testing.T) {
	restoreDefaultLogger(t)
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_AUTH_MODE", "off")
	t.Setenv("KAFKITO_INSECURE_AUTH_OFF", "")
	t.Setenv("VCAP_APPLICATION", "")
	t.Setenv("PORT", "0")

	assert.Equal(t, 2, run(""))
}

// Startup exits with 2 when no mode is configured. The "off" default itself is
// pinned by the config tests.
func TestRun_ReturnsExitCode2_WhenAuthModeIsUnset(t *testing.T) {
	restoreDefaultLogger(t)
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")
	require.NoError(t, os.Unsetenv("KAFKITO_AUTH_MODE"))
	t.Setenv("KAFKITO_INSECURE_AUTH_OFF", "")
	t.Setenv("VCAP_APPLICATION", "")
	t.Setenv("PORT", "0")

	assert.Equal(t, 2, run(""))
}

// A failed OpenID Connect discovery fails startup before the server listens.
func TestRun_ReturnsExitCode2_WhenOIDCDiscoveryFails(t *testing.T) {
	restoreDefaultLogger(t)
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_AUTH_MODE", "oidc")
	t.Setenv("KAFKITO_AUTH_OIDC_ISSUER_URL", "http://127.0.0.1:1")
	t.Setenv("KAFKITO_AUTH_OIDC_AUDIENCE", "kafkito-api")
	t.Setenv("KAFKITO_AUTH_OIDC_JWKS_URL", "")
	t.Setenv("PORT", "0")

	assert.Equal(t, 2, run(""))
}

func TestAuthModeConfig_MapsTheOIDCSettings(t *testing.T) {
	got := authModeConfig(config.AppAuthConfig{
		Mode: "oidc",
		OIDC: config.OIDCAuthConfig{
			IssuerURL:   "https://idp.example.com",
			Audience:    "kafkito-api",
			JWKSURL:     "https://idp.example.com/certs",
			RequiredTyp: "at+jwt",
			AllowedAZP:  []string{"proxy"},
		},
	})

	assert.Equal(t, "oidc", got.Mode)
	assert.Equal(t, auth.OIDCConfig{
		IssuerURL:    "https://idp.example.com",
		Audience:     "kafkito-api",
		JWKSEndpoint: "https://idp.example.com/certs",
		RequiredTyp:  "at+jwt",
		AllowedAZP:   []string{"proxy"},
	}, got.OIDC)
}
