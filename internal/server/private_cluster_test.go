// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/pkg/config"
	kafkapkg "github.com/FinkeFlo/kafkito/pkg/kafka"
)

func encodeHeader(t *testing.T, cfg config.ClusterConfig) string {
	t.Helper()

	b, err := json.Marshal(cfg)
	require.NoError(t, err, "marshal cluster config")
	return base64.StdEncoding.EncodeToString(b)
}

func TestDecodePrivateClusterHeader_AcceptsValidConfig(t *testing.T) {
	t.Parallel()

	good := config.ClusterConfig{
		Name:    "mine",
		Brokers: []string{"localhost:9092"},
		Auth:    config.AuthConfig{Type: "none"},
	}

	got, err := decodePrivateClusterHeader(encodeHeader(t, good))

	require.NoError(t, err)
	require.Len(t, got.Brokers, 1)
	assert.Equal(t, "localhost:9092", got.Brokers[0])
}

func TestDecodePrivateClusterHeader_RejectsInvalidEncoding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "not_base64",
			raw:  "!!!",
		},
		{
			name: "bad_json",
			raw:  base64.StdEncoding.EncodeToString([]byte("{not json")),
		},
		{
			name: "no_brokers",
			raw:  base64.StdEncoding.EncodeToString([]byte(`{"name":"x"}`)),
		},
		{
			name: "empty_broker",
			raw:  base64.StdEncoding.EncodeToString([]byte(`{"name":"x","brokers":[""]}`)),
		},
		{
			name: "bad_auth",
			raw:  base64.StdEncoding.EncodeToString([]byte(`{"name":"x","brokers":["a:1"],"auth":{"type":"plain"}}`)),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodePrivateClusterHeader(tc.raw)

			assert.Error(t, err, "decodePrivateClusterHeader(%q) must reject", tc.name)
		})
	}
}

func TestPrivateClusterMiddleware_StoresDecodedConfigInContext_WhenHeaderValid(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{Name: "x", Brokers: []string{"a:1"}}
	var seen bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok := privateClusterFromContext(r.Context())
		require.True(t, ok, "ctx must carry decoded config")
		require.Len(t, got.Brokers, 1)
		assert.Equal(t, "a:1", got.Brokers[0])
		seen = true
	})
	h := privateClusterMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))

	h.ServeHTTP(httptest.NewRecorder(), req)

	assert.True(t, seen, "next handler must run on valid header")
}

func TestPrivateClusterMiddleware_Returns400_WhenHeaderNotBase64(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler must not run on malformed header")
	})
	h := privateClusterMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(PrivateClusterHeader, "!!!not-base64!!!")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegistryUseAdhoc(t *testing.T) {
	t.Parallel()

	// Sub-tests share `reg` and `n1` across calls — they MUST run sequentially.
	// Do NOT add t.Parallel() to the inner t.Run blocks: the second and third
	// rows compare their result against `n1` returned by the first call.
	reg := kafkapkg.NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{
		Name:    "ignored",
		Brokers: []string{"b:1", "a:0"},
		Auth:    config.AuthConfig{Type: "none"},
	}

	n1, err := reg.UseAdhoc(cfg)
	require.NoError(t, err, "first UseAdhoc")

	t.Run("returns_adhoc_prefixed_name", func(t *testing.T) {
		assert.True(t, strings.HasPrefix(n1, kafkapkg.AdhocPrefix),
			"name = %q, want adhoc prefix %q", n1, kafkapkg.AdhocPrefix)
	})

	t.Run("same_fingerprint_when_only_display_name_changes", func(t *testing.T) {
		cfg2 := cfg
		cfg2.Name = "different-display-name"

		n2, err := reg.UseAdhoc(cfg2)

		require.NoError(t, err, "second UseAdhoc")
		assert.Equal(t, n1, n2, "display-name change must not affect fingerprint")
	})

	t.Run("different_fingerprint_when_brokers_change", func(t *testing.T) {
		cfg3 := cfg
		cfg3.Brokers = []string{"other:9092"}

		n3, err := reg.UseAdhoc(cfg3)

		require.NoError(t, err, "third UseAdhoc")
		assert.NotEqual(t, n1, n3, "broker change must yield different fingerprint")
	})
}

func TestResolvePrivateClusterParam_RewritesSentinelToFingerprint_WhenRouted(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	cfg := config.ClusterConfig{Brokers: []string{"x:1"}}
	expected, err := reg.UseAdhoc(cfg)
	require.NoError(t, err, "seed adhoc registration")

	var captured string
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware)
			g.Use(resolvePrivateClusterParam(reg))
			g.Get("/clusters/{cluster}/topics", func(_ http.ResponseWriter, req *http.Request) {
				captured = chi.URLParam(req, "cluster")
			})
		})
	})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, expected, captured, "sentinel must be rewritten to adhoc fingerprint")
}

func TestResolvePrivateClusterParam_Returns400_WhenHeaderMissing(t *testing.T) {
	t.Parallel()

	reg := kafkapkg.NewRegistry(nil, slog.Default())
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware)
			g.Use(resolvePrivateClusterParam(reg))
			g.Get("/clusters/{cluster}/topics", func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("handler must not run without header")
			})
		})
	})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestConfigValidateRejectsReservedNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		clusterName string
	}{
		{
			name:        "private_cluster_sentinel",
			clusterName: config.PrivateClusterSentinel,
		},
		{
			name:        "adhoc_prefixed_name",
			clusterName: config.AdhocClusterPrefix + "abc",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := config.Config{Clusters: []config.ClusterConfig{
				{Name: tc.clusterName, Brokers: []string{"a:1"}},
			}}

			err := c.Validate()

			assert.Error(t, err, "Validate must reject reserved cluster name %q", tc.clusterName)
		})
	}
}
