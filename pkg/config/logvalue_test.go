// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package config

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// logToJSONAttrs renders one slog attribute (key=value) through a JSON handler
// and returns the captured output. The returned string is the full log line so
// callers can assert on the masking behaviour applied to nested fields.
func logToJSONAttrs(t *testing.T, key string, value any) string {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.LogAttrs(context.Background(), slog.LevelInfo, "test", slog.Any(key, value))

	return buf.String()
}

func TestLogValue_MasksSecretsInJSONOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		key         string
		value       any
		wantAbsent  []string
		wantPresent []string
	}{
		{
			name:        "auth_config_masks_password",
			key:         "auth",
			value:       AuthConfig{Type: "plain", Username: "alice", Password: "s3cret"},
			wantAbsent:  []string{"s3cret"},
			wantPresent: []string{`"password":"***"`},
		},
		{
			name:       "schema_registry_config_masks_password",
			key:        "sr",
			value:      SchemaRegistryConfig{URL: "https://sr.example", Username: "u", Password: "pw"},
			wantAbsent: []string{`"pw"`},
		},
		{
			name: "cluster_config_hides_nested_passwords",
			key:  "cluster",
			value: ClusterConfig{
				Name:           "prod",
				Brokers:        []string{"b1:9092"},
				Auth:           AuthConfig{Type: "plain", Username: "u", Password: "leaky-password"},
				SchemaRegistry: SchemaRegistryConfig{Password: "another-leaky"},
			},
			wantAbsent: []string{"leaky"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := logToJSONAttrs(t, tc.key, tc.value)

			for _, secret := range tc.wantAbsent {
				assert.NotContains(t, got, secret, "secret leaked in log output: %s", got)
			}
			for _, masked := range tc.wantPresent {
				assert.Contains(t, got, masked, "expected masked value in log output: %s", got)
			}
		})
	}
}
