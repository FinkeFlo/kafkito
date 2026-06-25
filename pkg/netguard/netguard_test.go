// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/netguard"
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
		{"224.0.0.1", true},       // multicast
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
			assert.Equal(t, tc.blocked, netguard.BlockedIP(addr))
		})
	}
}

func TestGuardedDialContext_RefusesLoopbackLiteral(t *testing.T) {
	t.Parallel()

	dial := netguard.GuardedDialContext(nil)
	_, err := dial(context.Background(), "tcp", "127.0.0.1:0")

	require.Error(t, err)
	assert.True(t, errors.Is(err, netguard.ErrBlockedAddress),
		"expected ErrBlockedAddress, got: %v", err)
}

func TestGuardedDialContext_RefusesMetadataLiteral(t *testing.T) {
	t.Parallel()

	dial := netguard.GuardedDialContext(nil)
	_, err := dial(context.Background(), "tcp", "169.254.169.254:80")

	require.Error(t, err)
	assert.True(t, errors.Is(err, netguard.ErrBlockedAddress),
		"expected ErrBlockedAddress, got: %v", err)
}
