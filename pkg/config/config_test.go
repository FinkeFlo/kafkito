// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultsLoad(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, ":37421", cfg.Server.Addr)
	assert.Empty(t, cfg.Clusters)
}

func TestEnvShortcutSynthesizesCluster(t *testing.T) {
	t.Setenv("KAFKITO_KAFKA_BROKERS", "localhost:39092,kafka:9092")

	cfg, err := Load("")
	require.NoError(t, err)
	require.Len(t, cfg.Clusters, 1)
	assert.Equal(t, "local", cfg.Clusters[0].Name)
	assert.Equal(t, []string{"localhost:39092", "kafka:9092"}, cfg.Clusters[0].Brokers)
}

func TestYAMLConfigLoads(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(`
server:
  addr: ":9999"
clusters:
  - name: dev
    brokers:
      - localhost:39092
  - name: staging
    brokers:
      - kafka-stg:9092
`), 0o600))

	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":9999", cfg.Server.Addr)
	require.Len(t, cfg.Clusters, 2)
	assert.Equal(t, "dev", cfg.Clusters[0].Name)
	assert.Equal(t, "staging", cfg.Clusters[1].Name)
}

func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(`server:
  addr: ":1111"
`), 0o600))

	t.Setenv("KAFKITO_SERVER_ADDR", ":2222")
	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":2222", cfg.Server.Addr)
}

func TestDuplicateClusterRejected(t *testing.T) {
	cfg := Config{
		Clusters: []ClusterConfig{
			{Name: "dup", Brokers: []string{"a"}},
			{Name: "dup", Brokers: []string{"b"}},
		},
	}
	assert.Error(t, cfg.Validate())
}

func TestClusterWithoutBrokersRejected(t *testing.T) {
	cfg := Config{Clusters: []ClusterConfig{{Name: "x"}}}
	assert.Error(t, cfg.Validate())
}

func TestClusterByName(t *testing.T) {
	cfg := Config{Clusters: []ClusterConfig{
		{Name: "a", Brokers: []string{"1"}},
		{Name: "b", Brokers: []string{"2"}},
	}}
	got, ok := cfg.ClusterByName("b")
	assert.True(t, ok)
	assert.Equal(t, []string{"2"}, got.Brokers)

	_, ok = cfg.ClusterByName("missing")
	assert.False(t, ok)
}

func TestAuthValidation(t *testing.T) {
	tests := []struct {
		name    string
		auth    AuthConfig
		wantErr bool
	}{
		{"none-empty", AuthConfig{}, false},
		{"none-explicit", AuthConfig{Type: "none"}, false},
		{"plain-ok", AuthConfig{Type: "plain", Username: "u", Password: "p"}, false},
		{"plain-missing-pw", AuthConfig{Type: "plain", Username: "u"}, true},
		{"scram256-ok", AuthConfig{Type: "scram-sha-256", Username: "u", Password: "p"}, false},
		{"scram512-ok", AuthConfig{Type: "scram-sha-512", Username: "u", Password: "p"}, false},
		{"unknown-type", AuthConfig{Type: "kerberos"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Clusters: []ClusterConfig{{
				Name:    "c",
				Brokers: []string{"x"},
				Auth:    tt.auth,
			}}}
			err := cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAuthModeEnvBinding(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "mock")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Auth.Mode)
}

func TestAuthModeDefaultsEmpty(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Auth.Mode)
}

func TestRedacted(t *testing.T) {
	c := ClusterConfig{
		Name:    "c",
		Brokers: []string{"b"},
		Auth:    AuthConfig{Type: "plain", Username: "u", Password: "secret"},
	}
	r := c.Redacted()
	assert.Equal(t, "secret", c.Auth.Password, "original must not be mutated")
	assert.Equal(t, "***", r.Auth.Password)
	assert.Equal(t, "u", r.Auth.Username)
}
