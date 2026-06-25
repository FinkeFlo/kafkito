// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/netguard"
)

func TestSchemaRegistryClient_GuardedRefusesLoopbackDial(t *testing.T) {
	t.Parallel()

	c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: "http://127.0.0.1:9/"}, true)
	err := c.do(context.Background(), http.MethodGet, "/subjects", nil, nil)

	require.Error(t, err)
	assert.True(t, errors.Is(err, netguard.ErrBlockedAddress),
		"expected ErrBlockedAddress, got: %v", err)
}

func TestSchemaRegistryClient_UnguardedReachesLoopback(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/subjects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL}, false)
	var out []string
	err := c.do(context.Background(), http.MethodGet, "/subjects", nil, &out)

	require.NoError(t, err, "unguarded client must reach loopback httptest server")
}

func TestSubjectConfig_FallsBackToGlobalOnlyOnRealNotFound(t *testing.T) {
	t.Parallel()

	t.Run("subject_404_falls_back_to_global", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/config/orders", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error_code":40401,"message":"Subject not found."}`))
		})
		mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"compatibilityLevel":"BACKWARD"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL}, false)

		cfg, err := c.SubjectConfig(context.Background(), "orders")

		require.NoError(t, err)
		assert.Equal(t, "BACKWARD", cfg.CompatibilityLevel)
	})

	t.Run("server_error_containing_40401_is_not_swallowed", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/config/x40401y", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error_code":50001,"message":"internal error ref 40401"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL}, false)

		_, err := c.SubjectConfig(context.Background(), "x40401y")

		require.Error(t, err, "a 500 mentioning 40401 must NOT be treated as not-found")
		assert.Contains(t, err.Error(), "internal error")
	})
}

func TestSchemaRegistryDo_RefusesBasicAuthOverPlaintextHTTP(t *testing.T) {
	t.Parallel()

	c := newSchemaRegistryClient(config.SchemaRegistryConfig{
		URL:      "http://sr.internal:8081",
		Username: "user",
		Password: "secret",
	}, false)

	err := c.do(context.Background(), http.MethodGet, "/subjects", nil, nil)

	require.Error(t, err, "credentials over http must be refused before sending")
	assert.True(t, errors.Is(err, ErrInsecureSchemaRegistryAuth),
		"want ErrInsecureSchemaRegistryAuth, got %v", err)
}
