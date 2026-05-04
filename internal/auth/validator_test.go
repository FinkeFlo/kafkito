// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"testing"

	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// newTokFromClaims builds an unsigned, in-memory jwt.Token with the given
// claims. The TokString / TokStringSlice helpers operate on a parsed token,
// not the wire bytes, so signing and JWKS round-trips would only add latency
// without exercising any new branch.
func newTokFromClaims(t *testing.T, claims map[string]any) jwt.Token {
	t.Helper()

	tok := jwt.New()
	for k, v := range claims {
		require.NoError(t, tok.Set(k, v), "set %q", k)
	}
	return tok
}

// TestHostBelongsToDomain pins the security contract used by IdP-specific
// validators (notably xsuaa) to constrain JKU URLs to an allow-list domain.
// The function returns true iff the parsed host equals domain or sits one
// or more labels below it; the comparison is case-insensitive; URL parse
// failures return false. The sibling-suffix row is the load-bearing CVE
// guard — without the leading dot in the suffix check, an attacker host
// "evil-example.com" would falsely match the allow-list "example.com".
func TestHostBelongsToDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rawURL string
		domain string
		want   bool
	}{
		{name: "equal_host", rawURL: "https://example.com/jwks", domain: "example.com", want: true},
		{name: "one_label_below", rawURL: "https://auth.example.com/keys", domain: "example.com", want: true},
		{name: "two_labels_below", rawURL: "https://eu.auth.example.com/keys", domain: "example.com", want: true},
		{name: "sibling_suffix_attacker", rawURL: "https://evil-example.com/keys", domain: "example.com", want: false},
		{name: "sibling_suffix_no_dot", rawURL: "https://authexample.com/keys", domain: "example.com", want: false},
		{name: "case_mismatch_in_host", rawURL: "https://AUTH.Example.COM/keys", domain: "example.com", want: true},
		{name: "case_mismatch_in_domain", rawURL: "https://auth.example.com/keys", domain: "EXAMPLE.COM", want: true},
		{name: "with_port", rawURL: "https://auth.example.com:8443/keys", domain: "example.com", want: true},
		{name: "malformed_url", rawURL: "://not-a-url", domain: "example.com", want: false},
		{name: "empty_url", rawURL: "", domain: "example.com", want: false},
		{name: "empty_domain_does_not_wildcard", rawURL: "https://auth.example.com/keys", domain: "", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := auth.HostBelongsToDomain(tc.rawURL, tc.domain)

			assert.Equal(t, tc.want, got)
		})
	}
}

// TestTokString locks the contract that TokString returns ok=false on either
// missing claim OR wrong-type claim, and ok=true for any string-typed claim
// (including the empty string). The wrong-type rows guard against any future
// "tolerant" rewrite that silently coerces numbers / arrays into strings —
// that would be a security smell at the auth layer.
func TestTokString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		claims  map[string]any
		key     string
		wantStr string
		wantOK  bool
	}{
		{
			name:    "string_claim_present",
			claims:  map[string]any{"name": "alice"},
			key:     "name",
			wantStr: "alice",
			wantOK:  true,
		},
		{
			name:    "string_claim_empty_value_round_trips",
			claims:  map[string]any{"name": ""},
			key:     "name",
			wantStr: "",
			wantOK:  true,
		},
		{
			name:    "claim_missing",
			claims:  map[string]any{},
			key:     "name",
			wantStr: "",
			wantOK:  false,
		},
		{
			name:    "claim_wrong_type_int",
			claims:  map[string]any{"count": 42},
			key:     "count",
			wantStr: "",
			wantOK:  false,
		},
		{
			name:    "claim_wrong_type_array",
			claims:  map[string]any{"roles": []any{"a"}},
			key:     "roles",
			wantStr: "",
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok := newTokFromClaims(t, tc.claims)

			gotStr, gotOK := auth.TokString(tok, tc.key)

			assert.Equal(t, tc.wantStr, gotStr)
			assert.Equal(t, tc.wantOK, gotOK)
		})
	}
}

// TestTokStringSlice locks the dual-format contract: prefer JSON string array,
// fall back to space-separated string (the OIDC "scope" wire encoding), and
// return nil — not empty slice — when the claim is missing or empty.
//
// The empty-but-present-array row preserves a meaningful contract: an empty
// scope array is a real signal (the IdP issued a token with no scopes) that
// must NOT be conflated with a missing claim.
func TestTokStringSlice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		claims  map[string]any
		key     string
		want    []string
		wantNil bool
	}{
		{
			name:   "string_array_simple",
			claims: map[string]any{"scope": []any{"a", "b"}},
			key:    "scope",
			want:   []string{"a", "b"},
		},
		{
			name:   "string_array_mixed_filters_non_strings",
			claims: map[string]any{"scope": []any{"a", 123, "b", true}},
			key:    "scope",
			want:   []string{"a", "b"},
		},
		{
			name:   "space_separated_string_oidc_style",
			claims: map[string]any{"scope": "read write admin"},
			key:    "scope",
			want:   []string{"read", "write", "admin"},
		},
		{
			name:   "multiple_spaces_collapse_via_strings_fields",
			claims: map[string]any{"scope": "read   write"},
			key:    "scope",
			want:   []string{"read", "write"},
		},
		{
			name:    "empty_string_returns_nil",
			claims:  map[string]any{"scope": ""},
			key:     "scope",
			wantNil: true,
		},
		{
			name:   "empty_array_returns_empty_not_nil",
			claims: map[string]any{"scope": []any{}},
			key:    "scope",
			want:   []string{},
		},
		{
			name:    "claim_missing_returns_nil",
			claims:  map[string]any{},
			key:     "scope",
			wantNil: true,
		},
		{
			name:    "claim_wrong_type_int_returns_nil",
			claims:  map[string]any{"scope": 42},
			key:     "scope",
			wantNil: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok := newTokFromClaims(t, tc.claims)

			got := auth.TokStringSlice(tok, tc.key)

			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
