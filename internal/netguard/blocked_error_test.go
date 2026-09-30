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

	"github.com/FinkeFlo/kafkito/internal/netguard"
)

// The guarded dialer reports a refused dial as *BlockedAddressError. It
// still matches ErrBlockedAddress, and its text is unchanged from the
// former fmt.Errorf("%w: host %q -> %s") so existing log greps keep working.
func TestGuardedDial_BlockedAddressError(t *testing.T) {
	t.Parallel()

	dialed := false
	dial := netguard.GuardedDialWith(
		stubResolver("10.0.0.7", "::1"),
		func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("must not dial")
		},
	)

	_, err := dial(context.Background(), "tcp", "localhost:39092")

	require.ErrorIs(t, err, netguard.ErrBlockedAddress)
	assert.False(t, dialed, "a blocked resolution must refuse before any dial")
	var be *netguard.BlockedAddressError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, "localhost:39092", be.Addr)
	assert.Equal(t, "localhost", be.Host)
	assert.Equal(t, netip.MustParseAddr("::1"), be.IP)
	assert.Equal(t,
		`dial refused: address resolves to a blocked range: host "localhost" -> ::1`,
		err.Error())
}

func TestGuardedDialContext_LiteralBlockedAddressError(t *testing.T) {
	t.Parallel()

	_, err := netguard.GuardedDialContext(nil)(context.Background(), "tcp", "127.0.0.1:9092")

	var be *netguard.BlockedAddressError
	require.ErrorAs(t, err, &be)
	assert.Equal(t, "127.0.0.1", be.Host)
	// The system resolver may return the IPv4-mapped form; IP keeps it as
	// resolved (the error text always did), callers Unmap when they compare.
	assert.Equal(t, netip.MustParseAddr("127.0.0.1"), be.IP.Unmap())
}
