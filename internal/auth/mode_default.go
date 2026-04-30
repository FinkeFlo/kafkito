// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

// init registers the modes that are always part of the default (no-tag) build:
//   - "mock": generic OIDC validator + in-process JWKS fixture
//   - "off":  unavailable in default builds; the devauth build tag re-registers
//     "off" with a synthetic-principal validator (see mode_devauth.go)
//
// IdP-specific modes register themselves from their own subpackages behind
// build tags; package auth itself never imports them (which would form an
// import cycle).
func init() {
	Register("mock", newMockMode)
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
