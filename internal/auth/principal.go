// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package auth provides OIDC-based authentication primitives: a Validator
// interface plus a generic OIDCValidator implementation, principal extraction
// from a verified JWT, chi middleware that runs the validation, and helpers
// for scope-based authorisation. IdP-specific validators live in sub-packages
// and register themselves with the mode registry behind build tags. See
// `oidc_validator.go` for the generic JWT validation pipeline and `mode.go`
// for runtime mode selection.
package auth

import "context"

// Principal is the authenticated caller as derived from a verified OIDC token
// (or a synthetic value injected by the dev/mock modes).
type Principal struct {
	Subject    string   // sub
	Email      string   // email (may be empty depending on IdP)
	UserName   string   // user_name (or preferred_username)
	GivenName  string   // given_name
	FamilyName string   // family_name
	Origin     string   // origin (IdP key)
	Tenant     string   // tenant identifier (claim shape varies per IdP)
	Scopes     []string // local scope names (IdP-specific prefixes stripped)
}

// HasScope reports whether the principal carries the given local scope name.
func (p *Principal) HasScope(name string) bool {
	for _, s := range p.Scopes {
		if s == name {
			return true
		}
	}
	return false
}

type principalCtxKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext extracts the principal previously stored via WithPrincipal.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(*Principal)
	return p, ok
}
