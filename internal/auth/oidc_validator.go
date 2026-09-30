// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// OIDCConfig configures a generic OIDC JWT validator.
type OIDCConfig struct {
	// IssuerURL is the expected "iss" claim. Tokens whose iss is not exactly
	// this string are rejected; nothing is normalized, so a trailing slash
	// counts.
	IssuerURL string
	// Audience is the expected "aud" claim. Tokens whose aud claim does not
	// contain this value are rejected.
	Audience string
	// JWKSEndpoint is the URL serving the issuer's JSON Web Key Set used to
	// verify token signatures. Required by NewOIDCValidator, which does not
	// auto-discover; the "oidc" mode factory resolves it via OpenID Connect
	// discovery when unset.
	JWKSEndpoint string
	// RequiredTyp, when set, is the "typ" header every token must carry, for
	// example "at+jwt" (RFC 9068). It is compared case-insensitively and an
	// "application/" prefix is ignored on either side (RFC 7515 section
	// 4.1.9). Empty disables the check.
	RequiredTyp string
	// AllowedAZP, when non-empty, lists the "azp" (authorized party) values a
	// token may carry, compared exactly; a token without azp is rejected.
	// Empty disables the check.
	AllowedAZP []string
}

var (
	// ErrTypMismatch rejects a token whose typ header is not the required one.
	ErrTypMismatch = errors.New("token typ does not match the required typ")
	// ErrAZPNotAllowed rejects a token whose azp claim is missing or not listed.
	ErrAZPNotAllowed = errors.New("azp is not an allowed authorized party")
)

// OIDCValidator validates asymmetrically signed JWTs against a fixed
// issuer/audience and a JWKS endpoint. It backs both the "mock" and the
// generic "oidc" modes and works with any OIDC IdP that publishes a JWKS URL.
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

// Config returns the settings the validator enforces.
func (o *OIDCValidator) Config() OIDCConfig { return o.cfg }

// Validate parses and signature-verifies raw (see ParseToken for the
// algorithm and exp/nbf/sub rules), then enforces iss, aud and, when
// configured, typ and azp.
func (o *OIDCValidator) Validate(ctx context.Context, raw string) (*Principal, error) {
	if raw == "" {
		return nil, ErrTokenEmpty
	}

	kid, typ := tokenHeader(raw)
	set, err := o.keys.Keys(ctx, kid)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}

	tok, err := ParseToken(raw, set)
	if err != nil {
		return nil, err
	}

	if iss, ok := tok.Issuer(); !ok || iss != o.cfg.IssuerURL {
		return nil, ErrIssuerMismatch
	}

	if auds, _ := tok.Audience(); len(auds) == 0 {
		return nil, ErrAudienceMissing
	}
	if !AudienceContains(tok, o.cfg.Audience) {
		return nil, ErrAudienceMismatch
	}

	// ParseToken verified the signature over the protected header, so typ
	// (read from the same raw token) is authentic here.
	if o.cfg.RequiredTyp != "" && normalizeTyp(typ) != normalizeTyp(o.cfg.RequiredTyp) {
		return nil, ErrTypMismatch
	}
	if len(o.cfg.AllowedAZP) > 0 {
		azp, ok := TokString(tok, "azp")
		if !ok || !slices.Contains(o.cfg.AllowedAZP, azp) {
			return nil, ErrAZPNotAllowed
		}
	}

	return oidcPrincipalFromToken(tok), nil
}

// tokenHeader returns the kid and typ of raw's first signature's protected
// header, or "" for each when raw is not a JWS or lacks the header. It
// verifies nothing: the kid only picks the key to wait for, and Validate
// checks typ after the signature has been verified.
func tokenHeader(raw string) (kid, typ string) {
	msg, err := jws.Parse([]byte(raw))
	if err != nil || len(msg.Signatures()) == 0 {
		return "", ""
	}
	hdr := msg.Signatures()[0].ProtectedHeaders()
	kid, _ = hdr.KeyID()
	typ, _ = hdr.Type()
	return kid, typ
}

// normalizeTyp lower-cases a typ value and drops an "application/" prefix,
// the comparison RFC 7515 section 4.1.9 recommends.
func normalizeTyp(typ string) string {
	typ = strings.ToLower(strings.TrimSpace(typ))
	return strings.TrimPrefix(typ, "application/")
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
