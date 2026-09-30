// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discoveryPath is the OpenID Provider metadata path below an issuer.
const discoveryPath = "/.well-known/openid-configuration"

// discoveryServer serves body(serverURL) with status at the discovery path.
func discoveryServer(t *testing.T, status int, body func(issuer string) string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body(srv.URL)))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscoveryURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://idp.example.com/realms/k/.well-known/openid-configuration",
		discoveryURL("https://idp.example.com/realms/k"))
	assert.Equal(t, "https://tenant.example.com/.well-known/openid-configuration",
		discoveryURL("https://tenant.example.com/"))
}

func TestDiscoverJWKSURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		body    func(issuer string) string
		wantErr string
	}{
		{"ok", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `","jwks_uri":"` + iss + `/certs"}`
		}, ""},
		{"issuer_mismatch", http.StatusOK, func(string) string {
			return `{"issuer":"https://other.example.com","jwks_uri":"https://other.example.com/certs"}`
		}, "does not match"},
		{"issuer_trailing_slash", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `/","jwks_uri":"` + iss + `/certs"}`
		}, "does not match"},
		{"missing_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `"}`
		}, "no jwks_uri"},
		{"relative_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `","jwks_uri":"/certs"}`
		}, "absolute"},
		{"http_remote_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `","jwks_uri":"http://idp.example.com/certs"}`
		}, "https"},
		{"issuer_value_not_echoed", http.StatusOK, func(string) string {
			return `{"issuer":"https://attacker-chosen.example.com","jwks_uri":"https://x.example.com/certs"}`
		}, "does not match configured issuer"},
		{"userinfo_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `","jwks_uri":"https://` + testUserInfo + `@idp.example.com/certs"}`
		}, "user info"},
		{"bad_json", http.StatusOK, func(string) string { return `not json` }, "decode"},
		{"body_over_cap", http.StatusOK, func(iss string) string {
			return `{"pad":"` + strings.Repeat("x", maxDiscoveryBody) + `","issuer":"` + iss + `","jwks_uri":"` + iss + `/certs"}`
		}, "decode"},
		{"not_found", http.StatusNotFound, func(string) string { return `{}` }, "unexpected status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := discoveryServer(t, tc.status, tc.body)

			got, err := discoverJWKSURL(context.Background(), newDiscoveryClient(nil), srv.URL)

			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, srv.URL+"/certs", got)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.NotContains(t, err.Error(), "attacker-chosen", "the metadata issuer must not be echoed")
		})
	}
}

func TestDiscoverJWKSURL_HonoursContextTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := discoverJWKSURL(ctx, newDiscoveryClient(nil), srv.URL)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestNewDiscoveryClient_HasATimeout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DiscoveryTimeout, newDiscoveryClient(nil).Timeout)
}

// An https issuer must not be able to redirect discovery to plain http, not
// even to a loopback host.
func TestDiscoverJWKSURL_RejectsRedirectFromHTTPSToHTTP(t *testing.T) {
	t.Parallel()

	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(plain.Close)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+discoveryPath, http.StatusFound)
	}))
	t.Cleanup(tlsSrv.Close)

	_, err := discoverJWKSURL(context.Background(), newDiscoveryClient(tlsSrv.Client().Transport), tlsSrv.URL)

	require.Error(t, err)
	require.ErrorContains(t, err, "redirect")
	assert.Zero(t, plainHits.Load(), "the http target must not be requested")
}

// A redirect target must meet the same URL rule as the issuer: https, or
// plain http only on a loopback host.
func TestDiscoverJWKSURL_RejectsRedirectToRemoteHTTP(t *testing.T) {
	t.Parallel()

	var remoteHits atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		remoteHits.Add(1)
		return nil, errors.New("remote request made")
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://idp.example.com"+discoveryPath, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	client := newDiscoveryClient(splitTransport{loopback: srv.Client().Transport, other: transport})

	_, err := discoverJWKSURL(context.Background(), client, srv.URL)

	require.Error(t, err)
	require.ErrorContains(t, err, "https")
	assert.Zero(t, remoteHits.Load(), "the remote http target must not be requested")
}

func TestDiscoverJWKSURL_StopsAfterTooManyRedirects(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, discoveryPath, http.StatusFound) // to itself
	}))
	t.Cleanup(srv.Close)

	_, err := discoverJWKSURL(context.Background(), newDiscoveryClient(nil), srv.URL)

	require.ErrorContains(t, err, "redirects")
}

func TestCheckIdPURL(t *testing.T) {
	t.Parallel()

	ok := []string{
		"https://idp.example.com/realms/k",
		"https://idp.example.com:8443/certs",
		"http://localhost:8080/certs",
		"http://127.0.0.1:8080",
		"http://[::1]/certs",
	}
	for _, raw := range ok {
		require.NoError(t, checkIdPURL(raw), raw)
	}
	bad := map[string]string{
		"/certs":                       "absolute",
		"idp.example.com":              "absolute",
		"http://idp.example.com/certs": "https",
		"ftp://idp.example.com":        "https",
		"https://" + testUserInfo + "@idp.example.com/x": "user info",
		"http://localhost.example.com/x":                 "https",
		"http://localhost./x":                            "https",
		"http://[::1%25lo0]/x":                           "https",
		"http://127.1/x":                                 "https",
		"https://" + testUserInfoSecret + "@idp:bad":     "absolute",
	}
	for raw, want := range bad {
		err := checkIdPURL(raw)
		require.ErrorContains(t, err, want, raw)
		assert.NotContains(t, err.Error(), "secret", raw)
	}
}

func TestCheckIssuerURL_RejectsQueryAndFragment(t *testing.T) {
	t.Parallel()

	require.NoError(t, checkIssuerURL("https://idp.example.com/realms/k"))
	for _, raw := range []string{"https://idp.example.com/?x=1", "https://idp.example.com/#f", "https://idp.example.com/?"} {
		require.ErrorContains(t, checkIssuerURL(raw), "query or fragment", raw)
	}
}

// Metadata from an https issuer must not point the key fetch at plain http,
// not even on a loopback host.
func TestDiscoverJWKSURL_RejectsHTTPJWKSURIFromHTTPSIssuer(t *testing.T) {
	t.Parallel()

	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + srv.URL + `","jwks_uri":"http://127.0.0.1:9/certs"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := discoverJWKSURL(context.Background(), newDiscoveryClient(srv.Client().Transport), srv.URL)

	require.ErrorContains(t, err, "must use https")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// splitTransport sends loopback requests to loopback and all others to other.
type splitTransport struct{ loopback, other http.RoundTripper }

func (s splitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if isLoopbackHost(r.URL.Hostname()) {
		return s.loopback.RoundTrip(r)
	}
	return s.other.RoundTrip(r)
}

// Test user info for URLs, kept out of URL literals so secret scanners do not
// flag the fixtures.
const (
	testUserInfo       = "u" + ":p"
	testUserInfoSecret = "u" + ":secret"
)
