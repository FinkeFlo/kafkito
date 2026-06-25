// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/netguard"
)

// stubResolver returns a fixed slice of IPs for any host, simulating a
// multi-address DNS response without real DNS lookups.
func stubResolver(addrs ...string) func(ctx context.Context, network, host string) ([]netip.Addr, error) {
	parsed := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		parsed = append(parsed, netip.MustParseAddr(a))
	}
	return func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return parsed, nil
	}
}

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

// TestGuardedDialContext_FailoverToSecondIP verifies that when the first
// resolved IP cannot be dialed, GuardedDialContext falls over to the second IP
// and returns a successful connection.
//
// The test is fully hermetic: a stub resolver returns two private RFC-1918
// addresses (allowed by BlockedIP), and a stub dialOne returns an error for the
// first address and succeeds (returning a pipe conn) for the second. No real DNS
// or external network is required.
func TestGuardedDialContext_FailoverToSecondIP(t *testing.T) {
	t.Parallel()

	const firstIP = "10.0.0.1"
	const secondIP = "10.0.0.2"
	const port = "80"

	// stubConn is a real *net.Conn obtained from net.Pipe() so the caller
	// receives a valid, closeable connection without any real listening socket.
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	dialOne := func(_ context.Context, _, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		if host == firstIP {
			return nil, errors.New("stub: first address unreachable")
		}
		// Second IP succeeds; return the client side of the pipe.
		return clientConn, nil
	}

	resolve := stubResolver(firstIP, secondIP)
	dial := netguard.GuardedDialWithForTest(resolve, dialOne)

	conn, err := dial(context.Background(), "tcp", "myfakehost.internal:"+port)
	require.NoError(t, err, "dialer must fall over to the second IP and succeed")
	conn.Close()
}
