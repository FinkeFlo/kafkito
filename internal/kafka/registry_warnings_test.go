// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
)

// A configured cluster that uses SASL/PLAIN without TLS, or TLS without
// certificate verification, gets one warning per setting when the registry
// is built and none when its clients are built. The warning names the
// cluster and carries neither credentials nor broker addresses.
func TestNewRegistry_WarnsAboutTransportSettings(t *testing.T) {
	t.Parallel()

	const canary = "configured-canary-value"
	plain := func(authType string) config.AuthConfig {
		return config.AuthConfig{Type: authType, Username: "u", Password: canary}
	}
	clusters := []config.ClusterConfig{
		{Name: "plain-no-tls", Brokers: []string{"10.0.0.1:9092"}, Auth: plain("plain")},
		{Name: "plain-padded-upper", Brokers: []string{"10.0.0.2:9092"}, Auth: plain(" PLAIN ")},
		{Name: "plain-tls", Brokers: []string{"10.0.0.3:9092"}, Auth: plain("plain"), TLS: config.TLSConfig{Enabled: true}},
		{Name: "scram-no-tls", Brokers: []string{"10.0.0.4:9092"}, Auth: plain("scram-sha-256")},
		{Name: "skip-verify", Brokers: []string{"10.0.0.5:9092"}, TLS: config.TLSConfig{Enabled: true, InsecureSkipVerify: true}},
		{Name: "plain-skip-verify", Brokers: []string{"10.0.0.6:9092"}, Auth: plain("plain"), TLS: config.TLSConfig{Enabled: true, InsecureSkipVerify: true}},
		{Name: "skip-verify-tls-off", Brokers: []string{"10.0.0.7:9092"}, TLS: config.TLSConfig{InsecureSkipVerify: true}, Auth: plain("scram-sha-512")},
		{Name: "no-auth", Brokers: []string{"10.0.0.8:9092"}},
	}
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	reg := NewRegistry(clusters, logger)
	t.Cleanup(reg.Close)
	for _, c := range clusters {
		clientOpts(c, logger.With("cluster", c.Name))
	}

	warned := map[string][]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		assert.Equal(t, "WARN", rec["level"], line)
		cluster, _ := rec["cluster"].(string)
		msg, _ := rec["msg"].(string)
		warned[msg] = append(warned[msg], cluster)
	}
	assert.Equal(t, map[string][]string{
		"SASL/PLAIN without TLS: user name, password and records are sent unencrypted": {"plain-no-tls", "plain-padded-upper"},
		"TLS certificate verification disabled (insecure_skip_verify=true)":            {"skip-verify", "plain-skip-verify"},
	}, warned)
	assert.NotContains(t, logs.String(), canary)
	assert.NotContains(t, logs.String(), "10.0.0.")
}
