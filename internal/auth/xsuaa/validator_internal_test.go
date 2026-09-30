//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCapTestValidator(t *testing.T) *Validator {
	t.Helper()

	x, err := newValidator(Credentials{
		ClientID:  "sb-kafkito!t1",
		URL:       "https://provider.auth.example.com",
		UAADomain: "auth.example.com",
		XSAppName: "kafkito!t1",
	}, false)
	require.NoError(t, err)
	t.Cleanup(x.Close)
	return x
}

func TestValidator_OwnJKU_IsRegisteredAtConstruction(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)

	assert.Equal(t, "https://provider.auth.example.com/token_keys", x.ownJKU)
	assert.Len(t, x.sources, 1)
}

func TestValidator_CanonicalJKU_AcceptsDNSNamesOnPort443(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "upper_case_scheme_and_host",
			raw:  "HTTPS://Provider.Auth.Example.com/token_keys",
			want: "https://provider.auth.example.com/token_keys",
		},
		{
			name: "port_443_dropped",
			raw:  "https://provider.auth.example.com:443/token_keys",
			want: "https://provider.auth.example.com/token_keys",
		},
		{
			name: "empty_port_dropped",
			raw:  "https://provider.auth.example.com:/token_keys",
			want: "https://provider.auth.example.com/token_keys",
		},
		{
			name: "uaadomain_itself",
			raw:  "https://auth.example.com/token_keys",
			want: "https://auth.example.com/token_keys",
		},
		{
			// Punycode labels are plain LDH, so they pass; raw Unicode does not.
			name: "punycode_label",
			raw:  "https://xn--caf-dma.auth.example.com/token_keys",
			want: "https://xn--caf-dma.auth.example.com/token_keys",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			x := newCapTestValidator(t)

			got, err := x.canonicalJKU(tc.raw)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidator_CanonicalJKU_RejectsOutsidePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "ipv6_zone_literal", raw: "https://[::ffff:10.0.0.1%25x.auth.example.com]/token_keys", wantErr: errJKUHost},
		{name: "ipv6_zone_literal_double_escaped", raw: "https://[::ffff:10.0.0.1%2525x.auth.example.com]:8443/token_keys", wantErr: errJKUHost},
		{name: "ipv6_loopback_literal", raw: "https://[::1]/token_keys", wantErr: errJKUHost},
		{name: "ipv6_literal", raw: "https://[2001:db8::1]/token_keys", wantErr: errJKUHost},
		{name: "ipv4_mapped_ipv6_literal", raw: "https://[::ffff:10.0.0.1]/token_keys", wantErr: errJKUHost},
		{name: "ipv4_literal", raw: "https://10.0.0.1/token_keys", wantErr: errJKUHost},
		{name: "trailing_dot", raw: "https://t.auth.example.com./token_keys", wantErr: errJKUHost},
		{name: "empty_label", raw: "https://t..auth.example.com/token_keys", wantErr: errJKUHost},
		{name: "leading_hyphen", raw: "https://-t.auth.example.com/token_keys", wantErr: errJKUHost},
		{name: "label_over_63", raw: "https://" + strings.Repeat("a", 64) + ".auth.example.com/token_keys", wantErr: errJKUHost},
		{name: "non_ascii_host", raw: "https://é.auth.example.com/token_keys", wantErr: errJKUHost},
		{name: "port_8443", raw: "https://t.auth.example.com:8443/token_keys", wantErr: errJKUPort},
		{name: "outside_domain", raw: "https://evil.example/token_keys", wantErr: errJKUNotOwned},
		{name: "domain_lookalike", raw: "https://evilauth.example.com/token_keys", wantErr: errJKUNotOwned},
		{name: "http_non_loopback", raw: "http://t.auth.example.com/token_keys", wantErr: errJKUScheme},
		{name: "http_loopback_without_test_flag", raw: "http://127.0.0.1/token_keys", wantErr: errJKUScheme},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			x := newCapTestValidator(t)

			got, err := x.canonicalJKU(tc.raw)

			require.ErrorIs(t, err, tc.wantErr)
			assert.Empty(t, got)
			assert.NotContains(t, err.Error(), tc.raw, "errors name the rule, never the jku")
		})
	}
}

func TestValidator_CanonicalJKU_Port443SharesTheOwnSlot(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)
	own := x.sources[x.ownJKU]

	jku, err := x.canonicalJKU("https://provider.auth.example.com:443/token_keys")
	require.NoError(t, err)
	src, err := x.keySource(jku)

	require.NoError(t, err)
	assert.Same(t, own, src, "a token jku with :443 must reuse the own key source")
	assert.Len(t, x.sources, 1)
}

func TestValidator_KeySource_CapsDistinctJKUs(t *testing.T) {
	t.Parallel()

	x := newCapTestValidator(t)

	// The own jku occupies one slot; 15 more fit.
	for i := range maxJKUs - 1 {
		_, err := x.keySource(fmt.Sprintf("https://t%d.auth.example.com/token_keys", i))
		require.NoError(t, err, "jku %d is within the cap", i)
	}
	_, err := x.keySource("https://one-too-many.auth.example.com/token_keys")
	require.ErrorIs(t, err, errJKUTooMany)

	_, err = x.keySource("https://t0.auth.example.com/token_keys")
	require.NoError(t, err, "known jku URLs keep working at the cap")
	_, err = x.keySource(x.ownJKU)
	require.NoError(t, err, "the own jku keeps working at the cap")
	assert.Len(t, x.sources, maxJKUs)
}
