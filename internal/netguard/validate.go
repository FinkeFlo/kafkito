// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// ValidateHost resolves host (a "host" or "host:port") and returns an error
// if it is empty or any resolved address is blocked (see BlockedIP).
//
// This is a pre-flight check that turns obviously unsafe destinations into a
// friendly validation error before any connection is attempted. It is not
// the enforcement point: DNS can change between this check and the dial, so
// outbound connections must still go through GuardedDialContext.
func ValidateHost(host string) error {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty host")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if BlockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address", host)
		}
		return nil
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, a := range addrs {
		addr, perr := netip.ParseAddr(a)
		if perr != nil {
			continue
		}
		if BlockedIP(addr) {
			return fmt.Errorf("host %q resolves to a blocked address %s", host, a)
		}
	}
	return nil
}

// ValidateURL parses raw, requires an http(s) scheme, and validates its host
// with ValidateHost. Like ValidateHost it is a pre-flight check only.
func ValidateURL(raw string) error {
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
	return ValidateHost(u.Host)
}
