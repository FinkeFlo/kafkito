// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/auth"
)

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
	srv = httptest.NewUnstartedServer(mux)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscoveryURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://idp.example.com/realms/k/.well-known/openid-configuration",
		auth.DiscoveryURL("https://idp.example.com/realms/k"))
	assert.Equal(t, "https://tenant.example.com/.well-known/openid-configuration",
		auth.DiscoveryURL("https://tenant.example.com/"))
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
		{"missing_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `"}`
		}, "no jwks_uri"},
		{"relative_jwks_uri", http.StatusOK, func(iss string) string {
			return `{"issuer":"` + iss + `","jwks_uri":"/certs"}`
		}, "absolute"},
		{"bad_json", http.StatusOK, func(string) string { return `not json` }, "decode"},
		{"not_found", http.StatusNotFound, func(string) string { return `{}` }, "unexpected status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := discoveryServer(t, tc.status, tc.body)

			got, err := auth.DiscoverJWKSURL(context.Background(), srv.Client(), srv.URL)

			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, srv.URL+"/certs", got)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
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
	_, err := auth.DiscoverJWKSURL(ctx, srv.Client(), srv.URL)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
