// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// metadataIP is the cloud instance-metadata address (AWS/GCP/Azure).
var metadataIP = netip.MustParseAddr("169.254.169.254")

// BlockedIP reports whether an outbound connection to ip must be refused to
// prevent SSRF: loopback, link-local (incl. the cloud metadata endpoint),
// multicast, and unspecified addresses are blocked. Private RFC-1918 ranges are
// intentionally allowed (legitimate private clusters live there).
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	switch {
	case ip == metadataIP:
		return true
	case ip.IsLoopback():
		return true
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true
	case ip.IsUnspecified():
		return true
	case ip.IsMulticast():
		return true
	default:
		return false
	}
}

// ErrBlockedAddress is returned by the guarded dialer when a resolved address is blocked.
var ErrBlockedAddress = errors.New("dial refused: address resolves to a blocked range")

// GuardedDialContext returns a DialContext that resolves the host, refuses the
// dial if ANY resolved address is blocked (matching the validation-time guard's
// all-addresses policy), and connects to the exact validated IP to close the
// resolve->dial TOCTOU window (DNS rebinding). base may be nil (a default
// dialer with a sane timeout is used).
func GuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = &net.Dialer{Timeout: 10 * time.Second}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("dial %s: no addresses", host)
		}
		for _, ip := range ips {
			if BlockedIP(ip) {
				return nil, fmt.Errorf("%w: host %q -> %s", ErrBlockedAddress, host, ip)
			}
		}
		// Dial the exact IP we validated, not the hostname (no re-resolution).
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
	}
}
