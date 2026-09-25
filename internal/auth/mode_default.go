// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"fmt"
)

// init registers the modes that are part of every build (default, btp and
// devauth):
//   - "mock": generic OIDC validator + in-process JWKS fixture
//   - "oidc": generic OIDC validator against an external issuer (issuer,
//     audience and optional JWKS URL from ModeConfig.OIDC; the JWKS URL is
//     discovered via /.well-known/openid-configuration when unset)
//   - "off":  unavailable in default builds; the devauth build tag re-registers
//     "off" with a synthetic-principal validator (see mode_devauth.go)
//
// IdP-specific modes register themselves from their own subpackages behind
// build tags; package auth itself never imports them (which would form an
// import cycle).
func init() {
	Register("mock", newMockMode)
	Register("oidc", newOIDCMode)
	Register("off", newOffMode)
}

// MockAudience is the audience claim expected by mock-mode tokens. The
// mockoidc fixture's Issue() helper sets `aud = clientID`, so callers minting
// tokens for mock-mode validation should use this string as the clientID.
const MockAudience = "mock-client"

// newOffMode is the default-build factory for the "off" mode. The devauth
// build tag overrides this registration with a synthetic-principal validator
// suitable for local development.
func newOffMode(_ ModeConfig) (Validator, func(), error) {
	return nil, nil, ErrModeUnavailable
}

// newMockMode constructs a generic OIDCValidator backed by an in-process
// MockOIDC fixture. It is intentionally IdP-agnostic: tokens carry no scope
// prefix and no zone claim. The cleanup function stops the embedded server.
func newMockMode(_ ModeConfig) (Validator, func(), error) {
	mock, err := NewMockOIDC()
	if err != nil {
		return nil, nil, err
	}
	v, err := NewOIDCValidator(OIDCConfig{
		IssuerURL:    mock.Server.URL,
		Audience:     MockAudience,
		JWKSEndpoint: mock.JKU(),
	})
	if err != nil {
		mock.Close()
		return nil, nil, err
	}
	return v, mock.Close, nil
}

// newOIDCMode constructs a generic OIDCValidator for an external issuer. When
// no JWKS URL is configured it is discovered from the issuer's OpenID Provider
// metadata, bounded by DiscoveryTimeout, so a misconfigured or unreachable
// issuer fails startup instead of the first request.
func newOIDCMode(cfg ModeConfig) (Validator, func(), error) {
	c := cfg.OIDC
	if c.IssuerURL == "" {
		return nil, nil, errors.New("oidc mode: issuer URL required (auth.oidc.issuer_url / KAFKITO_AUTH_OIDC_ISSUER_URL)")
	}
	if c.Audience == "" {
		return nil, nil, errors.New("oidc mode: audience required (auth.oidc.audience / KAFKITO_AUTH_OIDC_AUDIENCE)")
	}
	if c.JWKSEndpoint == "" {
		ctx, cancel := context.WithTimeout(context.Background(), DiscoveryTimeout)
		defer cancel()
		jwksURL, err := DiscoverJWKSURL(ctx, nil, c.IssuerURL)
		if err != nil {
			return nil, nil, fmt.Errorf("oidc mode: %w (set auth.oidc.jwks_url to skip discovery)", err)
		}
		c.JWKSEndpoint = jwksURL
	}
	v, err := NewOIDCValidator(c)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc mode: %w", err)
	}
	return v, func() {}, nil
}
