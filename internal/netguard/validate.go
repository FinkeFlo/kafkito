// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
)

// Errors of the pre-flight checks. Their texts are fixed: they never contain
// the checked host or URL, a resolved address or resolver details, so a
// caller may return them to the client that submitted the value.
var (
	// ErrEmptyHost: the host is empty or blank.
	ErrEmptyHost = errors.New("empty host")
	// ErrUnresolvable: the host name did not resolve to an address.
	ErrUnresolvable = errors.New("host name could not be resolved")
	// ErrHostNotAllowed: the host is, or resolves to, a blocked address
	// (see BlockedIP).
	ErrHostNotAllowed = errors.New("destination not allowed")
	// ErrInvalidURL: the URL does not parse.
	ErrInvalidURL = errors.New("invalid URL")
	// ErrURLScheme: the URL scheme is neither http nor https.
	ErrURLScheme = errors.New("URL scheme must be http or https")
	// ErrURLNoHost: the URL has no host.
	ErrURLNoHost = errors.New("URL has no host")
)

// LookupFunc resolves a host name to its addresses, like
// net.Resolver.LookupHost.
type LookupFunc func(ctx context.Context, host string) ([]string, error)

// HostValidator runs the pre-flight checks of ValidateHost and ValidateURL
// for one request. It keeps the result per host, so the definitions of a
// request that name the same host several times cost one lookup. It is safe
// for concurrent use; checks run one at a time.
//
// These checks turn obviously unsafe destinations into a validation error
// before any connection is attempted. They are not the enforcement point:
// DNS can change between the check and the dial, so outbound connections
// must still go through GuardedDialContext.
type HostValidator struct {
	lookup LookupFunc

	mu      sync.Mutex
	results map[string]error
}

// NewHostValidator returns a HostValidator that resolves host names with
// lookup, or with net.DefaultResolver when lookup is nil.
func NewHostValidator(lookup LookupFunc) *HostValidator {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	return &HostValidator{lookup: lookup, results: make(map[string]error)}
}

// Host checks host, a "host" or "host:port". It returns ErrEmptyHost,
// ErrUnresolvable, ErrHostNotAllowed when the host is or resolves to any
// blocked address (see BlockedIP), or nil.
func (v *HostValidator) Host(ctx context.Context, host string) error {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "" {
		return ErrEmptyHost
	}
	key := strings.ToLower(host)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err, ok := v.results[key]; ok {
		return err
	}
	err := v.check(ctx, host)
	v.results[key] = err
	return err
}

func (v *HostValidator) check(ctx context.Context, host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		if BlockedIP(addr) {
			return ErrHostNotAllowed
		}
		return nil
	}
	addrs, err := v.lookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return ErrUnresolvable
	}
	for _, a := range addrs {
		addr, perr := netip.ParseAddr(a)
		if perr != nil {
			continue
		}
		if BlockedIP(addr) {
			return ErrHostNotAllowed
		}
	}
	return nil
}

// URL parses raw, requires an http(s) scheme and checks its host with Host.
// It returns ErrInvalidURL, ErrURLScheme, ErrURLNoHost, an error of Host or
// nil.
func (v *HostValidator) URL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return ErrInvalidURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrURLScheme
	}
	if u.Host == "" {
		return ErrURLNoHost
	}
	return v.Host(ctx, u.Host)
}

// ValidateHost is HostValidator.Host for a single host, resolved with
// net.DefaultResolver.
func ValidateHost(host string) error {
	return NewHostValidator(nil).Host(context.Background(), host)
}

// ValidateURL is HostValidator.URL for a single URL, resolved with
// net.DefaultResolver.
func ValidateURL(raw string) error {
	return NewHostValidator(nil).URL(context.Background(), raw)
}
