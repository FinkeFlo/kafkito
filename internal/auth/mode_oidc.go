// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// init registers "oidc", the generic OIDC validator against an external
// issuer, which every build includes.
func init() {
	Register("oidc", newOIDCMode)
}

// newOIDCMode builds an OIDCValidator for the issuer in cfg.OIDC. Without a
// JWKS URL it discovers one from the issuer's OpenID Provider metadata,
// bounded by DiscoveryTimeout; a failed discovery fails startup. The keys are
// then loaded once: a failure is logged and startup continues, like the other
// JWT modes. The cleanup function closes the validator's key source.
func newOIDCMode(cfg ModeConfig) (Validator, func(), error) {
	c := cfg.OIDC
	if c.IssuerURL == "" {
		return nil, nil, errors.New("oidc mode: issuer URL required (auth.oidc.issuer_url / KAFKITO_AUTH_OIDC_ISSUER_URL)")
	}
	if c.Audience == "" {
		return nil, nil, errors.New("oidc mode: audience required (auth.oidc.audience / KAFKITO_AUTH_OIDC_AUDIENCE)")
	}
	if err := checkIssuerURL(c.IssuerURL); err != nil {
		return nil, nil, fmt.Errorf("oidc mode: issuer URL: %w", err)
	}
	if c.JWKSEndpoint != "" {
		if err := checkIdPURL(c.JWKSEndpoint); err != nil {
			return nil, nil, fmt.Errorf("oidc mode: JWKS URL: %w", err)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), DiscoveryTimeout)
		defer cancel()
		jwksURL, err := discoverJWKSURL(ctx, newDiscoveryClient(nil), c.IssuerURL)
		if err != nil {
			return nil, nil, fmt.Errorf("oidc mode: %w (set auth.oidc.jwks_url to skip discovery)", err)
		}
		c.JWKSEndpoint = jwksURL
	}

	v, err := NewOIDCValidator(c)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc mode: %w", err)
	}
	if err := v.WarmUp(context.Background()); err != nil {
		slog.Warn("auth: JWKS warm-up failed; startup continues", "mode", "oidc", "err", err)
	}
	return v, v.Close, nil
}
