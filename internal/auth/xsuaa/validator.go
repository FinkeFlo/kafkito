//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

// maxJKUs caps the distinct jku URLs a Validator keeps a key source for. The
// binding's own <url>/token_keys is registered at construction and counts.
const maxJKUs = 16

// tokenKeysPath is the only path XSUAA serves its key set at.
const tokenKeysPath = "/token_keys"

// jku policy errors. They name the rule, never the header value.
var (
	errJKUInvalid  = errors.New("jku is not an absolute URL")
	errJKUScheme   = errors.New("jku must use https")
	errJKUPath     = errors.New("jku path must be " + tokenKeysPath)
	errJKUExtras   = errors.New("jku must not carry user info, a query or a fragment")
	errJKUTooMany  = errors.New("jku rejected: too many distinct jku URLs")
	errNoOwnJKU    = errors.New("xsuaa: credentials url does not yield an allowed jku")
	errJKUNotOwned = errors.New("jku host outside uaadomain")
	errJKUHost     = errors.New("jku host must be a DNS name")
	errJKUPort     = errors.New("jku must not name a port other than 443")
)

// Validator validates RS256 tokens issued by an XSUAA tenant. Construct via
// NewValidator with the credentials parsed from VCAP_SERVICES, and Close it
// on shutdown.
type Validator struct {
	creds             Credentials
	allowLoopbackHTTP bool   // tests only: accept http jku URLs on loopback IPs
	ownJKU            string // canonical <creds.URL>/token_keys, "" if it breaks the policy

	mu      sync.Mutex
	sources map[string]*auth.KeySource // canonical jku -> key source, at most maxJKUs
	closed  bool
}

var _ auth.Validator = (*Validator)(nil)

// NewValidator wires a new validator. It does no I/O. Key sets are loaded
// per jku on first use (or by WarmUp for the binding's own jku), refreshed at
// most once per minute when a token names an unknown kid, and a request waits
// at most 5 s for them (see auth.KeySource). A jku must use https, belong to
// the uaadomain, have the path /token_keys and carry no query or fragment;
// at most 16 distinct jku URLs are kept.
func NewValidator(creds Credentials) (*Validator, error) {
	return newValidator(creds, false)
}

func newValidator(creds Credentials, allowLoopbackHTTP bool) (*Validator, error) {
	if creds.XSAppName == "" || creds.URL == "" || creds.UAADomain == "" {
		return nil, errors.New("xsuaa: missing required credentials")
	}
	if strings.ContainsAny(creds.UAADomain, ":/") {
		return nil, fmt.Errorf("UAADomain %q must be a bare hostname (no port, no scheme)", creds.UAADomain)
	}
	x := &Validator{
		creds:             creds,
		allowLoopbackHTTP: allowLoopbackHTTP,
		sources:           make(map[string]*auth.KeySource),
	}
	// Register the binding's own jku up front so the jku cap can never lock
	// out the bound tenant.
	if own, err := x.canonicalJKU(strings.TrimSuffix(creds.URL, "/") + tokenKeysPath); err == nil {
		x.ownJKU = own
		x.sources[own] = auth.NewKeySource(own)
	}
	return x, nil
}

// WarmUp loads the key set of the binding's own jku (<url>/token_keys),
// bounded by ctx and the key wait timeout. NewMode calls it at startup and
// only logs a failure.
func (x *Validator) WarmUp(ctx context.Context) error {
	if x.ownJKU == "" {
		return errNoOwnJKU
	}
	src, err := x.keySource(x.ownJKU)
	if err != nil {
		return err
	}
	_, err = src.Keys(ctx, "")
	return err
}

// Close releases all key sources. Validate fails afterwards.
func (x *Validator) Close() {
	x.mu.Lock()
	sources := x.sources
	x.sources = nil
	x.closed = true
	x.mu.Unlock()

	for _, src := range sources {
		src.Close()
	}
}

// Validate parses, signature-verifies, and applies XSUAA-specific claim checks.
func (x *Validator) Validate(ctx context.Context, raw string) (*auth.Principal, error) {
	if raw == "" {
		return nil, errors.New("empty bearer token")
	}

	// Parse JWS to read header (jku, kid) before fetching keys.
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse jws: %w", err)
	}
	if len(msg.Signatures()) == 0 {
		return nil, errors.New("token has no signatures")
	}
	hdr := msg.Signatures()[0].ProtectedHeaders()
	jkuStr, ok := hdr.JWKSetURL()
	if !ok || jkuStr == "" {
		return nil, errors.New("token missing jku header")
	}
	jku, err := x.canonicalJKU(jkuStr)
	if err != nil {
		return nil, err
	}
	src, err := x.keySource(jku)
	if err != nil {
		return nil, err
	}
	kid, _ := hdr.KeyID()
	set, err := src.Keys(ctx, kid)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}

	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(60*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}

	iss, issOK := tok.Issuer()
	if !issOK || (iss != x.creds.URL && !strings.HasPrefix(iss, x.creds.URL+"/")) {
		return nil, fmt.Errorf("iss %q not under %q", iss, x.creds.URL)
	}

	auds, _ := tok.Audience()
	if len(auds) == 0 {
		return nil, errors.New("aud claim missing")
	}
	if !auth.AudienceContains(tok, x.creds.ClientID) && !auth.AudienceContains(tok, x.creds.XSAppName) {
		return nil, fmt.Errorf("aud %v contains neither clientid nor xsappname", auds)
	}

	if zid, ok := auth.TokString(tok, "zid"); x.creds.IdentityZoneID != "" && (!ok || zid != x.creds.IdentityZoneID) {
		return nil, fmt.Errorf("zid %q != %q", zid, x.creds.IdentityZoneID)
	}

	return principalFromToken(tok, x.creds.LocalScopePrefix()), nil
}

// canonicalJKU applies the jku policy and returns the URL to fetch,
// https://host/token_keys with a lower-case host. The host must be a DNS name
// (LDH labels, so IP literals, zones and raw Unicode fail; punycode passes)
// equal to or under the uaadomain, on the default port 443. Plain http passes
// only for loopback IPs and only when allowLoopbackHTTP is set (tests); that
// case keeps the host's port.
func (x *Validator) canonicalJKU(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return "", errJKUInvalid
	}
	host := strings.ToLower(u.Hostname())
	loopback := false
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && x.allowLoopbackHTTP && isLoopbackIP(host):
		loopback = true
	default:
		return "", errJKUScheme
	}
	if !loopback && !isDNSName(host) {
		return "", errJKUHost
	}
	domain := strings.ToLower(x.creds.UAADomain)
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return "", fmt.Errorf("%w %q", errJKUNotOwned, x.creds.UAADomain)
	}
	if port := u.Port(); !loopback && port != "" && port != "443" {
		return "", errJKUPort
	}
	if u.Path != tokenKeysPath {
		return "", errJKUPath
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errJKUExtras
	}
	if loopback {
		return "http://" + strings.ToLower(u.Host) + tokenKeysPath, nil
	}
	return (&url.URL{Scheme: "https", Host: host, Path: tokenKeysPath}).String(), nil
}

// dnsLabel is one LDH label: letters, digits and inner hyphens, 1-63 bytes.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// allDigits matches a label of digits only.
var allDigits = regexp.MustCompile(`^[0-9]+$`)

// isDNSName reports whether host is a lower-case DNS name of LDH labels
// joined by single dots, without a trailing dot, at most 253 bytes long.
// A last label of digits only is refused (RFC 3696 section 2), so dotted
// IPv4 literals, which are LDH too, do not pass.
func isDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return !allDigits.MatchString(labels[len(labels)-1])
}

// keySource returns the key source for a canonical jku, creating it while
// fewer than maxJKUs exist.
func (x *Validator) keySource(jku string) (*auth.KeySource, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return nil, auth.ErrKeySourceClosed
	}
	if src, ok := x.sources[jku]; ok {
		return src, nil
	}
	if len(x.sources) >= maxJKUs {
		return nil, errJKUTooMany
	}
	src := auth.NewKeySource(jku)
	x.sources[jku] = src
	return src, nil
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
