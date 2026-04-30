//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"strings"

	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// principalFromToken builds an auth.Principal from a verified XSUAA token,
// stripping the "<xsappname>." prefix from each scope so callers see local
// scope names (e.g. "Display" rather than "kafkito!t12345.Display").
func principalFromToken(tok jwt.Token, scopePrefix string) *auth.Principal {
	p := &auth.Principal{}
	p.Subject, _ = tok.Subject()
	p.Email, _ = auth.TokString(tok, "email")
	p.UserName, _ = auth.TokString(tok, "user_name")
	p.GivenName, _ = auth.TokString(tok, "given_name")
	p.FamilyName, _ = auth.TokString(tok, "family_name")
	p.Origin, _ = auth.TokString(tok, "origin")
	p.Tenant, _ = auth.TokString(tok, "zid")
	for _, s := range auth.TokStringSlice(tok, "scope") {
		if strings.HasPrefix(s, scopePrefix) {
			p.Scopes = append(p.Scopes, strings.TrimPrefix(s, scopePrefix))
		}
	}
	return p
}
