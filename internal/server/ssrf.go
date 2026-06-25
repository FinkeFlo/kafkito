// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/FinkeFlo/kafkito/pkg/netguard"
)

// blockedIP reports whether an outbound connection to ip must be refused to
// prevent SSRF. Delegates to netguard.BlockedIP.
func blockedIP(ip netip.Addr) bool {
	return netguard.BlockedIP(ip)
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
