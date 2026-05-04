// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

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
		name    string
		rawURL  string
		domain  string
		want    bool
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
