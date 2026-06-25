// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

func TestBlockedIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},       // loopback
		{"::1", true},             // loopback v6
		{"169.254.169.254", true}, // cloud metadata (link-local)
		{"169.254.1.1", true},     // link-local
		{"0.0.0.0", true},         // unspecified
		{"10.0.0.5", false},       // private but legitimate for private clusters
		{"192.168.1.10", false},   // private but legitimate
		{"172.16.0.1", false},     // private (RFC-1918 third range) but legitimate
		{"8.8.8.8", false},        // public
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			addr, err := netip.ParseAddr(tc.ip)
			require.NoError(t, err)
			assert.Equal(t, tc.blocked, blockedIP(addr))
		})
	}
}

func TestValidateOutboundURL(t *testing.T) {
	t.Parallel()

	assert.Error(t, validateOutboundURL("http://127.0.0.1:8081"), "loopback must be blocked")
	assert.Error(t, validateOutboundURL("http://169.254.169.254/latest/meta-data/"), "metadata must be blocked")
	assert.Error(t, validateOutboundURL("ftp://example.com"), "non-http(s) scheme must be blocked")
	assert.Error(t, validateOutboundURL("://nonsense"), "unparseable url must be blocked")
	assert.NoError(t, validateOutboundURL("https://203.0.113.10:8081"), "public host must pass")
}

func TestValidatePrivateClusterConfig_BlocksSSRFSchemaRegistry(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Brokers: []string{"203.0.113.10:9092"},
		Auth:    config.AuthConfig{Type: "none"},
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "http://169.254.169.254/latest/meta-data/",
		},
	}

	err := validatePrivateClusterConfig(cfg)

	assert.Error(t, err, "SR URL pointing at the metadata endpoint must be rejected")
}
