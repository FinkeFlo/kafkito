// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DiscoveryTimeout bounds the OpenID Provider metadata request the "oidc"
	// mode makes at startup when no JWKS URL is configured.
	DiscoveryTimeout = 10 * time.Second
	// maxDiscoveryBody caps the metadata document size read from the issuer.
	maxDiscoveryBody = 1 << 20
	// maxDiscoveryRedirects caps the redirects followed during discovery.
	maxDiscoveryRedirects = 5
)

// discoveryURL returns the OpenID Connect Discovery 1.0 metadata URL for
// issuer ("<issuer>/.well-known/openid-configuration").
func discoveryURL(issuer string) string {
	return strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
}

// newDiscoveryClient returns the HTTP client for discovery: bounded by
// DiscoveryTimeout, following at most maxDiscoveryRedirects redirects, never
// from https to another scheme, and only to URLs checkIdPURL accepts. A nil
// transport means http.DefaultTransport; tests pass their own.
func newDiscoveryClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     transport,
		Timeout:       DiscoveryTimeout,
		CheckRedirect: checkDiscoveryRedirect,
	}
}

func checkDiscoveryRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxDiscoveryRedirects {
		return fmt.Errorf("stopped after %d redirects", maxDiscoveryRedirects)
	}
	if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return errors.New("redirect from https to another scheme not allowed")
	}
	if err := checkIdPURLParsed(req.URL); err != nil {
		return fmt.Errorf("redirect target: %w", err)
	}
	return nil
}

// discoverJWKSURL fetches the issuer's OpenID Provider metadata and returns
// its jwks_uri. The metadata's issuer must equal the configured issuer
// exactly (OpenID Connect Discovery 1.0 section 4.3), and jwks_uri must meet
// checkIdPURL and use https unless the issuer itself is plain http. The document is read up to maxDiscoveryBody bytes; callers
// bound the request via ctx and the client.
func discoverJWKSURL(ctx context.Context, client *http.Client, issuer string) (string, error) {
	endpoint := discoveryURL(issuer)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("oidc discovery: build request for %q: %w", endpoint, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery: GET %s: unexpected status %s", endpoint, resp.Status)
	}

	var meta struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBody)).Decode(&meta); err != nil {
		return "", fmt.Errorf("oidc discovery: decode %s: %w", endpoint, err)
	}
	if meta.Issuer != issuer {
		// The metadata value is not quoted: the IdP controls its size.
		return "", fmt.Errorf("oidc discovery: metadata issuer does not match configured issuer %q", issuer)
	}
	if meta.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery: %s has no jwks_uri", endpoint)
	}
	if err := checkIdPURL(meta.JWKSURI); err != nil {
		return "", fmt.Errorf("oidc discovery: jwks_uri: %w", err)
	}
	// Loopback http is for a local issuer only: metadata served by an https
	// issuer must not point the key fetch at a plain-http endpoint.
	if strings.HasPrefix(strings.ToLower(meta.JWKSURI), "http:") && !strings.HasPrefix(strings.ToLower(issuer), "http:") {
		return "", errors.New("oidc discovery: jwks_uri must use https when the issuer does")
	}
	return meta.JWKSURI, nil
}

// checkIdPURL requires an absolute https URL without user info; plain http
// is accepted only for loopback hosts, for tests and a local issuer. It
// applies to the configured issuer and JWKS URLs, the discovered jwks_uri and
// every discovery redirect.
func checkIdPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		// raw is not quoted: it may carry user info that url.Parse could not split off.
		return errors.New("URL must be an absolute URL")
	}
	return checkIdPURLParsed(u)
}

// checkIssuerURL applies checkIdPURL and additionally rejects a query or
// fragment, which OpenID Connect Discovery 1.0 section 3 forbids in an issuer.
func checkIssuerURL(raw string) error {
	if err := checkIdPURL(raw); err != nil {
		return err
	}
	if u, _ := url.Parse(raw); u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("%q must not contain a query or fragment", u.Redacted())
	}
	return nil
}

func checkIdPURLParsed(u *url.URL) error {
	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("%q must be an absolute URL", u.Redacted())
	}
	if u.User != nil {
		return fmt.Errorf("%q must not contain user info", u.Redacted())
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%q must use https (http only for loopback hosts)", u.Redacted())
}

// isLoopbackHost reports whether host is "localhost" or a loopback IP.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
