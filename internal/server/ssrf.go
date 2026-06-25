// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// metadataIP is the cloud instance-metadata address (AWS/GCP/Azure). It is
// link-local and therefore already covered by blockedIP, but named for clarity.
var metadataIP = netip.MustParseAddr("169.254.169.254")

// blockedIP reports whether an outbound connection to ip must be refused to
// prevent SSRF. Loopback, link-local (incl. the cloud metadata endpoint),
// multicast, and unspecified addresses are blocked. Private RFC-1918 ranges
// are intentionally allowed: legitimate private clusters live there.
func blockedIP(ip netip.Addr) bool {
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

// validateOutboundHost resolves host (a "host" or "host:port") and returns an
// error if it is empty or any resolved address is blocked.
func validateOutboundHost(host string) error {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty host")
	}
	// Literal IP: check directly.
	if addr, err := netip.ParseAddr(host); err == nil {
		if blockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address", host)
		}
		return nil
	}
	// Hostname: resolve and check every address.
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, a := range addrs {
		addr, perr := netip.ParseAddr(a)
		if perr != nil {
			continue
		}
		if blockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address %s", host, a)
		}
	}
	return nil
}

// validateOutboundURL parses raw, requires an http(s) scheme, and validates the
// host against the SSRF block-list.
func validateOutboundURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url scheme %q not allowed", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url has no host")
	}
	return validateOutboundHost(u.Host)
}
