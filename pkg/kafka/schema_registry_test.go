// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
)

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
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})

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
		c := newSchemaRegistryClient(config.SchemaRegistryConfig{URL: srv.URL})

		_, err := c.SubjectConfig(context.Background(), "x40401y")

		require.Error(t, err, "a 500 mentioning 40401 must NOT be treated as not-found")
		assert.Contains(t, err.Error(), "internal error")
	})
}
