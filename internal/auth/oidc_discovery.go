// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DiscoveryTimeout bounds the OpenID Provider metadata request made at
// startup when no JWKS URL is configured.
const DiscoveryTimeout = 10 * time.Second

// maxDiscoveryBody caps the metadata document size read from the issuer.
const maxDiscoveryBody = 1 << 20

// DiscoveryURL returns the OpenID Connect Discovery 1.0 metadata URL for
// issuer ("<issuer>/.well-known/openid-configuration").
func DiscoveryURL(issuer string) string {
	return strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
}

// DiscoverJWKSURL fetches the issuer's OpenID Provider metadata and returns
// its jwks_uri. The metadata's issuer must equal the configured issuer
// exactly, as required by OpenID Connect Discovery 1.0 §4.3. A nil client
// means http.DefaultClient; callers bound the request via ctx.
func DiscoverJWKSURL(ctx context.Context, client *http.Client, issuer string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := DiscoveryURL(issuer)
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
		return "", fmt.Errorf("oidc discovery: metadata issuer %q does not match configured issuer %q", meta.Issuer, issuer)
	}
	if meta.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery: %s has no jwks_uri", endpoint)
	}
	u, err := url.Parse(meta.JWKSURI)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("oidc discovery: jwks_uri %q is not an absolute URL", meta.JWKSURI)
	}
	return meta.JWKSURI, nil
}
