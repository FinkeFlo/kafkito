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

// BlockedAddressError is the error the guarded dialer returns when a resolved
// address is blocked. It unwraps to ErrBlockedAddress, so errors.Is keeps
// working, and carries the refused dial target for callers that report it.
type BlockedAddressError struct {
	// Addr is the host:port the dial was asked for.
	Addr string
	// Host is the host part of Addr, as given (name or IP literal).
	Host string
	// IP is the first resolved address that is blocked, as resolved.
	IP netip.Addr
}

// Error keeps the text of the former wrapped ErrBlockedAddress byte for byte.
func (e *BlockedAddressError) Error() string {
	return fmt.Sprintf("%s: host %q -> %s", ErrBlockedAddress, e.Host, e.IP)
}

// Unwrap makes errors.Is(err, ErrBlockedAddress) true.
func (e *BlockedAddressError) Unwrap() error { return ErrBlockedAddress }

// defaultResolver wraps net.DefaultResolver.LookupNetIP.
func defaultResolver(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// GuardedDialContext returns a DialContext that resolves the host, refuses the
// dial if ANY resolved address is blocked (matching the validation-time guard's
// all-addresses policy), and connects to the exact validated IPs in order to
// close the resolve->dial TOCTOU window (DNS rebinding) while preserving
// multi-address failover for HA hosts. base may be nil (a default dialer with
// a sane timeout is used).
//
// After confirming that no resolved address is blocked, each IP is tried in the
// order returned by the resolver; the first successful connection is returned.
// If every IP fails, the last dial error is returned (wrapped). The hostname is
// never re-resolved between attempts so the TOCTOU guarantee is preserved.
func GuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = &net.Dialer{Timeout: 10 * time.Second}
	}
	dialOne := func(ctx context.Context, network, ipAddr string) (net.Conn, error) {
		return base.DialContext(ctx, network, ipAddr)
	}
	return GuardedDialWith(defaultResolver, dialOne)
}

// GuardedDialWith is GuardedDialContext with an injectable resolver and per-IP
// dial function. Tests in other packages use it to point a guarded client at a
// local fake broker: the resolver maps a test host to an allowed address and
// dialOne redirects that address. The SSRF rules still apply to every
// resolved address, so a blocked address is refused before dialOne runs.
func GuardedDialWith(
	resolve func(ctx context.Context, network, host string) ([]netip.Addr, error),
	dialOne func(ctx context.Context, network, addr string) (net.Conn, error),
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolve(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("dial %s: no addresses", host)
		}
		// Refuse the whole dial if ANY resolved address is blocked (all-or-nothing
		// policy prevents split-brain rebinding where only some IPs are safe).
		for _, ip := range ips {
			if BlockedIP(ip) {
				return nil, &BlockedAddressError{Addr: addr, Host: host, IP: ip}
			}
		}
		// Try each validated IP in order; return the first successful connection.
		// We dial the IP literals directly — the hostname is never re-resolved —
		// so the TOCTOU guarantee is preserved across all attempts.
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialOne(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("dial %s: all addresses failed: %w", host, lastErr)
	}
}
