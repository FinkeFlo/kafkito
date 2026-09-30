// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// OIDCConfig configures a generic OIDC JWT validator.
type OIDCConfig struct {
	// IssuerURL is the expected "iss" claim. Tokens whose iss does not equal
	// this value (or, for compatibility with multi-tenant IdPs, sit immediately
	// under it via "<IssuerURL>/...") are rejected.
	IssuerURL string
	// Audience is the expected "aud" claim. Tokens whose aud claim does not
	// contain this value are rejected.
	Audience string
	// JWKSEndpoint is the URL serving the issuer's JSON Web Key Set used to
	// verify token signatures. Required: this generic validator does not
	// auto-discover via /.well-known/openid-configuration.
	JWKSEndpoint string
}

// OIDCValidator validates RS-signed JWTs against a fixed issuer/audience and
// a JWKS endpoint. Use this as the default "mock" mode validator, and as a
// drop-in for any OIDC IdP that publishes a JWKS URL.
type OIDCValidator struct {
	cfg  OIDCConfig
	keys *KeySource
}

var _ Validator = (*OIDCValidator)(nil)

// NewOIDCValidator constructs a validator. It does no I/O: the key set is
// loaded by WarmUp or the first Validate call, refreshed at most once per
// minute when a token names an unknown kid, and a request waits at most 5 s
// for it (see KeySource). Call Close to release the validator.
func NewOIDCValidator(cfg OIDCConfig) (*OIDCValidator, error) {
	if cfg.IssuerURL == "" {
		return nil, errors.New("OIDCValidator: IssuerURL required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("OIDCValidator: Audience required")
	}
	if cfg.JWKSEndpoint == "" {
		return nil, errors.New("OIDCValidator: JWKSEndpoint required")
	}
	return &OIDCValidator{cfg: cfg, keys: NewKeySource(cfg.JWKSEndpoint)}, nil
}

// WarmUp loads the key set once, bounded by ctx and the key wait timeout.
// Mode factories call it at startup and only log a failure; Validate
// retries the load once the refresh interval has passed.
func (o *OIDCValidator) WarmUp(ctx context.Context) error {
	_, err := o.keys.Keys(ctx, "")
	return err
}

// Close releases the key source. Validate fails afterwards.
func (o *OIDCValidator) Close() {
	o.keys.Close()
}

// Validate parses and signature-verifies raw, then enforces iss/aud/exp/nbf.
func (o *OIDCValidator) Validate(ctx context.Context, raw string) (*Principal, error) {
	if raw == "" {
		return nil, errors.New("empty bearer token")
	}

	set, err := o.keys.Keys(ctx, tokenKeyID(raw))
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
	if !issOK || (iss != o.cfg.IssuerURL && !strings.HasPrefix(iss, o.cfg.IssuerURL+"/")) {
		return nil, fmt.Errorf("iss %q not under %q", iss, o.cfg.IssuerURL)
	}

	auds, _ := tok.Audience()
	if len(auds) == 0 {
		return nil, errors.New("aud claim missing")
	}
	if !AudienceContains(tok, o.cfg.Audience) {
		return nil, fmt.Errorf("aud %v does not contain %q", auds, o.cfg.Audience)
	}

	return oidcPrincipalFromToken(tok), nil
}

// tokenKeyID returns the kid of raw's first signature, or "" when raw is not
// a JWS or names no kid. It verifies nothing; it only picks the key to wait for.
func tokenKeyID(raw string) string {
	msg, err := jws.Parse([]byte(raw))
	if err != nil || len(msg.Signatures()) == 0 {
		return ""
	}
	kid, _ := msg.Signatures()[0].ProtectedHeaders().KeyID()
	return kid
}

// oidcPrincipalFromToken builds a Principal without scope-prefix stripping.
// Scopes are read from "scope" (string or array) and merged with "scopes"
// (string array) so both common IdP shapes are accepted.
func oidcPrincipalFromToken(tok jwt.Token) *Principal {
	p := &Principal{}
	p.Subject, _ = tok.Subject()
	p.Email, _ = TokString(tok, "email")
	p.UserName, _ = TokString(tok, "user_name")
	if p.UserName == "" {
		p.UserName, _ = TokString(tok, "preferred_username")
	}
	p.GivenName, _ = TokString(tok, "given_name")
	p.FamilyName, _ = TokString(tok, "family_name")
	p.Origin, _ = TokString(tok, "origin")
	p.Tenant, _ = TokString(tok, "zid")

	seen := make(map[string]struct{})
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		p.Scopes = append(p.Scopes, s)
	}
	for _, s := range TokStringSlice(tok, "scope") {
		add(s)
	}
	for _, s := range TokStringSlice(tok, "scopes") {
		add(s)
	}
	return p
}
