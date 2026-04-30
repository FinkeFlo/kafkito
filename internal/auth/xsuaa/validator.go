//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// Validator validates RS256 tokens issued by an XSUAA tenant. Construct via
// NewValidator with the credentials parsed from VCAP_SERVICES.
type Validator struct {
	creds Credentials
	cache *jwk.Cache
}

var _ auth.Validator = (*Validator)(nil)

// NewValidator wires a new validator. The JWKS cache uses jwx defaults
// (refresh on unknown kid, soft TTL ~15 min).
func NewValidator(creds Credentials) (*Validator, error) {
	if creds.XSAppName == "" || creds.URL == "" || creds.UAADomain == "" {
		return nil, errors.New("xsuaa: missing required credentials")
	}
	if strings.ContainsAny(creds.UAADomain, ":/") {
		return nil, fmt.Errorf("UAADomain %q must be a bare hostname (no port, no scheme)", creds.UAADomain)
	}
	// httprc.NewClient() is not started here; jwk.NewCache starts it internally.
	client := httprc.NewClient()
	cache, err := jwk.NewCache(context.Background(), client)
	if err != nil {
		return nil, fmt.Errorf("jwk cache: %w", err)
	}
	return &Validator{creds: creds, cache: cache}, nil
}

// Validate parses, signature-verifies, and applies XSUAA-specific claim checks.
func (x *Validator) Validate(ctx context.Context, raw string) (*auth.Principal, error) {
	if raw == "" {
		return nil, errors.New("empty bearer token")
	}

	// Parse JWS to read header (jku, kid) before fetching keys.
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse jws: %w", err)
	}
	if len(msg.Signatures()) == 0 {
		return nil, errors.New("token has no signatures")
	}
	hdr := msg.Signatures()[0].ProtectedHeaders()
	jkuStr, ok := hdr.JWKSetURL()
	if !ok || jkuStr == "" {
		return nil, errors.New("token missing jku header")
	}
	if !auth.HostBelongsToDomain(jkuStr, x.creds.UAADomain) {
		return nil, fmt.Errorf("jku host outside uaadomain %q", x.creds.UAADomain)
	}

	// Register the JKU URL with the cache (idempotent — subsequent calls are no-ops).
	if !x.cache.IsRegistered(ctx, jkuStr) {
		if err := x.cache.Register(ctx, jkuStr); err != nil {
			return nil, fmt.Errorf("register jwks url: %w", err)
		}
	}
	set, err := x.cache.Lookup(ctx, jkuStr)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}

	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(60*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}

	iss, issOK := tok.Issuer()
	if !issOK || (iss != x.creds.URL && !strings.HasPrefix(iss, x.creds.URL+"/")) {
		return nil, fmt.Errorf("iss %q not under %q", iss, x.creds.URL)
	}

	auds, _ := tok.Audience()
	if len(auds) == 0 {
		return nil, errors.New("aud claim missing")
	}
	if !auth.AudienceContains(tok, x.creds.ClientID) && !auth.AudienceContains(tok, x.creds.XSAppName) {
		return nil, fmt.Errorf("aud %v contains neither clientid nor xsappname", auds)
	}

	if zid, ok := auth.TokString(tok, "zid"); x.creds.IdentityZoneID != "" && (!ok || zid != x.creds.IdentityZoneID) {
		return nil, fmt.Errorf("zid %q != %q", zid, x.creds.IdentityZoneID)
	}

	return principalFromToken(tok, x.creds.LocalScopePrefix()), nil
}
