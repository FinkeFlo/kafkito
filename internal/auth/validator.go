// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"net/url"
	"strings"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Validator authenticates a raw bearer token and returns a Principal.
type Validator interface {
	Validate(ctx context.Context, rawToken string) (*Principal, error)
}

// HostBelongsToDomain reports whether rawURL's host equals domain or sits one
// or more labels below it (e.g. "auth.example.com" belongs to "example.com").
// The comparison is case-insensitive. Used by IdP-specific validators that
// constrain JKU URLs to an allow-list domain.
func HostBelongsToDomain(rawURL, domain string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	domain = strings.ToLower(domain)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// AudienceContains reports whether tok's "aud" claim contains want.
func AudienceContains(tok jwt.Token, want string) bool {
	auds, _ := tok.Audience()
	for _, a := range auds {
		if a == want {
			return true
		}
	}
	return false
}

// TokString reads a string-typed claim by key. Returns ok=false if the key is
// missing or has a non-string value.
func TokString(tok jwt.Token, key string) (string, bool) {
	var s string
	if err := tok.Get(key, &s); err != nil {
		return "", false
	}
	return s, true
}

// TokStringSlice reads a claim that may be either a JSON string array or a
// space-separated string (the latter being how some OIDC IdPs encode "scope").
// Returns nil when the claim is missing or empty.
func TokStringSlice(tok jwt.Token, key string) []string {
	var raw []any
	if err := tok.Get(key, &raw); err == nil {
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	var s string
	if err := tok.Get(key, &s); err == nil && s != "" {
		return strings.Fields(s)
	}
	return nil
}
