// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

const plainWithoutTLSMsg = "SASL/PLAIN requires TLS for private clusters"

// plainWithoutTLSServer is server.New over a registry with the configured
// cluster "static", a short Test connection timeout and the given
// private_clusters settings. Copy requests go to a destCopyRegistry whose
// DescribeTopic reports a missing topic.
func plainWithoutTLSServer(t *testing.T, settings config.PrivateClustersConfig) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	reg := kafkapkg.NewRegistry([]config.ClusterConfig{{Name: "static", Brokers: []string{unreachableBroker}}}, logger)
	t.Cleanup(reg.Close)
	cfg := config.Defaults()
	cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
	cfg.PrivateClusters = settings
	return New(Options{
		Version: "test", Logger: logger, Registry: reg, Config: cfg,
		testConnLimiter: noTestConnLimit(),
		copyRegistry:    destCopyRegistry{err: kafkapkg.ErrTopicNotFound},
	})
}

// plainCluster is a private cluster definition with SASL/PLAIN of the given
// auth.type spelling.
func plainCluster(authType string, tls config.TLSConfig) config.ClusterConfig {
	return config.ClusterConfig{
		Brokers: []string{unreachableBroker},
		Auth:    config.AuthConfig{Type: authType, Username: "u", Password: "p"},
		TLS:     tls,
	}
}

// copyBody is a copy request into the private cluster cfg.
func copyBody(t *testing.T, cfg config.ClusterConfig) string {
	t.Helper()
	dest := map[string]any{
		"brokers": cfg.Brokers,
		"auth":    map[string]string{"type": cfg.Auth.Type, "username": cfg.Auth.Username, "password": cfg.Auth.Password},
		"tls":     map[string]bool{"enabled": cfg.TLS.Enabled, "insecure_skip_verify": cfg.TLS.InsecureSkipVerify},
	}
	b, err := json.Marshal(map[string]any{"dest_topic": "orders2", "dest_cluster_config": dest})
	require.NoError(t, err)
	return string(b)
}

// testBody is a Test connection body for the private cluster cfg.
func testBody(t *testing.T, cfg config.ClusterConfig) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"brokers": cfg.Brokers,
		"auth":    map[string]string{"type": cfg.Auth.Type, "username": cfg.Auth.Username, "password": cfg.Auth.Password},
		"tls":     map[string]bool{"enabled": cfg.TLS.Enabled, "insecure_skip_verify": cfg.TLS.InsecureSkipVerify},
	})
	require.NoError(t, err)
	return string(b)
}

const copyPath = "/api/v1/clusters/static/topics/orders/copy"

// A private cluster with SASL/PLAIN and TLS off is refused with 400
// plain_without_tls at every place a private cluster definition enters:
// the X-Kafkito-Cluster header on a cluster route, the header and the body
// of Test connection and dest_cluster_config of a copy. The header accepts
// auth.type case-insensitive and trimmed, so every spelling is refused.
// No broker is contacted.
func TestPrivateCluster_PlainWithoutTLSRefused(t *testing.T) {
	t.Parallel()

	h := plainWithoutTLSServer(t, config.PrivateClustersConfig{})
	headerWant := `{"code":"plain_without_tls","error":"X-Kafkito-Cluster: ` + plainWithoutTLSMsg + `"}`

	for _, authType := range []string{"plain", "PLAIN", " plain "} {
		header := encodeHeader(t, plainCluster(authType, config.TLSConfig{}))

		rec := sendCluster(h, http.MethodGet, "/api/v1/clusters/__private__/topics", "", header)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "header %q", authType)
		assert.JSONEq(t, headerWant, rec.Body.String(), "header %q", authType)

		rec = sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", header)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "test header %q", authType)
		assert.JSONEq(t, headerWant, rec.Body.String(), "test header %q", authType)
	}

	// The request validator holds a body to the lower-case auth.type enum.
	noTLS := plainCluster("plain", config.TLSConfig{})
	rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", testBody(t, noTLS), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"code":"plain_without_tls","error":"`+plainWithoutTLSMsg+`"}`, rec.Body.String())

	rec = sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", `{"brokers":["`+unreachableBroker+`"],"auth":{"type":"plain","username":"u","password":"p"}}`, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "test body without a tls block")
	assert.JSONEq(t, `{"code":"plain_without_tls","error":"`+plainWithoutTLSMsg+`"}`, rec.Body.String())

	rec = sendCluster(h, http.MethodPost, copyPath, copyBody(t, noTLS), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"code":"plain_without_tls","error":"dest_cluster_config: `+plainWithoutTLSMsg+`"}`, rec.Body.String())

	// TLS settings other than enabled do not count.
	skipOnly := plainCluster("plain", config.TLSConfig{InsecureSkipVerify: true})
	rec = sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, skipOnly))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, headerWant, rec.Body.String())
}

// The rule comes before the other checks of a definition: a PLAIN
// definition without TLS that also lacks a password or names a loopback
// broker gets plain_without_tls.
func TestPrivateCluster_PlainWithoutTLSCheckedFirst(t *testing.T) {
	t.Parallel()

	h := plainWithoutTLSServer(t, config.PrivateClustersConfig{})
	for name, cfg := range map[string]config.ClusterConfig{
		"no password": {Brokers: []string{unreachableBroker}, Auth: config.AuthConfig{Type: "plain", Username: "u"}},
		"loopback":    {Brokers: []string{"127.0.0.1:9092"}, Auth: config.AuthConfig{Type: "plain", Username: "u", Password: "p"}},
	} {
		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, cfg))
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		assert.JSONEq(t, `{"code":"plain_without_tls","error":"X-Kafkito-Cluster: `+plainWithoutTLSMsg+`"}`, rec.Body.String(), name)
	}
}

// SASL/PLAIN with TLS, also without certificate verification, and SCRAM
// or no authentication without TLS stay allowed at every entry point.
func TestPrivateCluster_PlainWithTLSAndScramAllowed(t *testing.T) {
	t.Parallel()

	h := plainWithoutTLSServer(t, config.PrivateClustersConfig{})
	scram := func(mechanism string) config.ClusterConfig {
		return config.ClusterConfig{
			Brokers: []string{unreachableBroker},
			Auth:    config.AuthConfig{Type: mechanism, Username: "u", Password: "p"},
		}
	}
	for name, cfg := range map[string]config.ClusterConfig{
		"plain with tls":              plainCluster("plain", config.TLSConfig{Enabled: true}),
		"plain with tls, skip verify": plainCluster("plain", config.TLSConfig{Enabled: true, InsecureSkipVerify: true}),
		"scram-sha-256 without tls":   scram("scram-sha-256"),
		"scram-sha-512 without tls":   scram("scram-sha-512"),
		"no auth without tls":         {Brokers: []string{unreachableBroker}, Auth: config.AuthConfig{Type: "none"}},
	} {
		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, cfg))
		assert.Equal(t, http.StatusOK, rec.Code, "%s header: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"reachable":false`, "%s header", name)

		rec = sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", testBody(t, cfg), "")
		assert.Equal(t, http.StatusOK, rec.Code, "%s body: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"reachable":false`, "%s body", name)

		rec = sendCluster(h, http.MethodPost, copyPath, copyBody(t, cfg), "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		assert.Contains(t, rec.Body.String(), `does not exist on cluster \"__private__\"`, "%s copy: %s", name, rec.Body.String())
	}
}

// private_clusters.allow_plain_without_tls lets SASL/PLAIN without TLS
// through every entry point: the requests then only fail on the
// connection (or, for the copy, on the missing destination topic).
func TestPrivateCluster_PlainWithoutTLSOptOut(t *testing.T) {
	t.Parallel()

	h := plainWithoutTLSServer(t, config.PrivateClustersConfig{AllowPlainWithoutTLS: true})
	assertPlainWithoutTLSAccepted(t, h)
}

// assertPlainWithoutTLSAccepted checks that h accepts a private cluster
// with SASL/PLAIN and TLS off in the header, the Test connection header
// and body and a copy's dest_cluster_config.
func assertPlainWithoutTLSAccepted(t *testing.T, h http.Handler) {
	t.Helper()
	for _, authType := range []string{"plain", "PLAIN", " plain "} {
		header := encodeHeader(t, plainCluster(authType, config.TLSConfig{}))

		rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", header)
		require.Equal(t, http.StatusOK, rec.Code, "test header %q: %s", authType, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"reachable":false`)
		assert.Contains(t, rec.Body.String(), `"auth_type":"plain"`)

		rec = sendPrivate(t, h, header, http.MethodGet, "/api/v1/clusters/__private__/topics", "", 300*time.Millisecond)
		assert.Equal(t, http.StatusBadGateway, rec.Code, "header %q: %s", authType, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "plain_without_tls")
	}

	noTLS := plainCluster("plain", config.TLSConfig{})
	rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", testBody(t, noTLS), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"reachable":false`)

	rec = sendCluster(h, http.MethodPost, copyPath, copyBody(t, noTLS), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `does not exist on cluster \"__private__\"`, rec.Body.String())
}

// The opt-out works when loaded from the YAML key and from the env var.
// Not parallel: it sets env vars.
func TestPrivateCluster_PlainWithoutTLSOptOutLoaded(t *testing.T) {
	for _, k := range []string{"KAFKITO_CONFIG", "KAFKITO_PRIVATE_CLUSTERS", "KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", "KAFKITO_KAFKA_BROKERS"} {
		t.Setenv(k, "")
	}
	serverFor := func(t *testing.T, cfg config.Config) http.Handler {
		t.Helper()
		logger := slog.New(slog.DiscardHandler)
		reg := kafkapkg.NewRegistry(cfg.Clusters, logger)
		t.Cleanup(reg.Close)
		cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
		return New(Options{
			Version: "test", Logger: logger, Registry: reg, Config: cfg,
			testConnLimiter: noTestConnLimit(),
			copyRegistry:    destCopyRegistry{err: kafkapkg.ErrTopicNotFound},
		})
	}
	writeConfig := func(t *testing.T, yaml string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "kafkito.yaml")
		require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
		return path
	}
	const clusters = "clusters:\n  - name: static\n    brokers: [\"" + unreachableBroker + "\"]\n"

	t.Run("default", func(t *testing.T) {
		cfg, err := config.Load(writeConfig(t, clusters))
		require.NoError(t, err)
		require.False(t, cfg.PrivateClusters.AllowPlainWithoutTLS)
		rec := sendCluster(serverFor(t, cfg), http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, plainCluster("plain", config.TLSConfig{})))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"code":"plain_without_tls"`)
	})
	t.Run("yaml", func(t *testing.T) {
		cfg, err := config.Load(writeConfig(t, clusters+"private_clusters:\n  allow_plain_without_tls: true\n"))
		require.NoError(t, err)
		assertPlainWithoutTLSAccepted(t, serverFor(t, cfg))
	})
	t.Run("env", func(t *testing.T) {
		t.Setenv("KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", "true")
		cfg, err := config.Load(writeConfig(t, clusters))
		require.NoError(t, err)
		assertPlainWithoutTLSAccepted(t, serverFor(t, cfg))
	})
	t.Run("env false beats yaml", func(t *testing.T) {
		t.Setenv("KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS", "false")
		cfg, err := config.Load(writeConfig(t, clusters+"private_clusters:\n  allow_plain_without_tls: true\n"))
		require.NoError(t, err)
		rec := sendCluster(serverFor(t, cfg), http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, plainCluster("plain", config.TLSConfig{})))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), `"code":"plain_without_tls"`)
	})
}

// The opt-out does not change the other rules for private clusters.
func TestPrivateCluster_PlainWithoutTLSOptOutKeepsOtherChecks(t *testing.T) {
	t.Parallel()

	h := plainWithoutTLSServer(t, config.PrivateClustersConfig{AllowPlainWithoutTLS: true})
	noPassword := config.ClusterConfig{Brokers: []string{unreachableBroker}, Auth: config.AuthConfig{Type: "plain", Username: "u"}}
	rec := sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, noPassword))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"X-Kafkito-Cluster: auth.username and auth.password are required for SASL"}`, rec.Body.String())

	loopback := plainCluster("plain", config.TLSConfig{})
	loopback.Brokers = []string{"127.0.0.1:9092"}
	rec = sendCluster(h, http.MethodPost, "/api/v1/clusters/_test", "", encodeHeader(t, loopback))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, rec.Body.String(), "plain_without_tls")
}
