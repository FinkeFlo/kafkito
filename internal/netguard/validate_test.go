// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/netguard"
)

func TestValidateHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host    string
		wantErr bool
	}{
		{"127.0.0.1", true},               // loopback literal
		{"127.0.0.1:9092", true},          // host:port form is split
		{"[::1]:9092", true},              // bracketed v6 literal
		{"169.254.169.254:80", true},      // cloud metadata
		{"0.0.0.0:9092", true},            // unspecified
		{"localhost:9092", true},          // hostname resolving to loopback
		{"", true},                        // empty
		{"   ", true},                     // blank
		{":9092", true},                   // port only
		{"203.0.113.10:9092", false},      // public literal
		{" 10.0.0.5:9092", false},         // surrounding whitespace trimmed
		{"192.168.1.10", false},           // RFC-1918 allowed
		{"host.invalid:9092", true},       // unresolvable (RFC 6761 .invalid)
		{"::ffff:127.0.0.1", true},        // v4-mapped loopback
		{"[::ffff:10.0.0.5]:9092", false}, // v4-mapped private
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			err := netguard.ValidateHost(tc.host)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateURL(t *testing.T) {
	t.Parallel()

	require.Error(t, netguard.ValidateURL("http://127.0.0.1:8081"), "loopback must be blocked")
	require.Error(t, netguard.ValidateURL("http://169.254.169.254/latest/meta-data/"), "metadata must be blocked")
	require.Error(t, netguard.ValidateURL("ftp://example.com"), "non-http(s) scheme must be blocked")
	require.Error(t, netguard.ValidateURL("://nonsense"), "unparseable url must be blocked")
	require.Error(t, netguard.ValidateURL("http:///path"), "url without host must be blocked")
	assert.NoError(t, netguard.ValidateURL("https://203.0.113.10:8081"), "public host must pass")
	assert.NoError(t, netguard.ValidateURL("HTTPS://203.0.113.10"), "scheme is case-insensitive")
}
