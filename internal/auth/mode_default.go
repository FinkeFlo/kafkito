// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"log/slog"
)

// init registers "mock", the generic OIDC validator backed by an in-process
// JWKS fixture, which every build includes. "off" is registered exactly once
// per build by a build-tagged file: mode_off.go (without devauth, where it is
// unavailable) or mode_devauth.go (with a synthetic-principal validator).
//
// IdP-specific modes register themselves from their own subpackages behind
// build tags; package auth itself never imports them (which would form an
// import cycle).
func init() {
	Register("mock", newMockMode)
}

// MockAudience is the audience claim expected by mock-mode tokens. The
// mockoidc fixture's Issue() helper sets `aud = clientID`, so callers minting
// tokens for mock-mode validation should use this string as the clientID.
const MockAudience = "mock-client"

// newMockMode constructs a generic OIDCValidator backed by an in-process
// MockOIDC fixture. It is intentionally IdP-agnostic: tokens carry no scope
// prefix and no zone claim. The keys are loaded at startup; a failure is
// logged and startup continues. The cleanup function closes the validator's
// key source and stops the embedded server.
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
	if err := v.WarmUp(context.Background()); err != nil {
		slog.Warn("auth: JWKS warm-up failed; startup continues", "mode", "mock", "err", err)
	}
	return v, func() {
		v.Close()
		mock.Close()
	}, nil
}
