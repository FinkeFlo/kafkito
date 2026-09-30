// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
)

// syncBuffer is a goroutine-safe log sink: the kafka client logs from its own
// goroutines while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

const leakPassword = "Sup3r-Secret-Leak-Canary"

// Private-cluster credentials travel in X-Kafkito-Cluster on every request.
// Neither the password nor the raw header value may show up in a response
// body or in any log line (request log, handler logs, kafka client logs) —
// for malformed headers, SSRF-rejected configs and well-formed configs whose
// brokers are unreachable.
//
// encodeHeader produces the same format as the frontend's
// encodePrivateClusterHeader: base64 of the JSON ClusterConfig.
func TestPrivateClusterHeader_NeverLeaksCredentials(t *testing.T) {
	t.Parallel()

	sasl := config.AuthConfig{Type: "plain", Username: "leak-user", Password: leakPassword}
	unreachable := encodeHeader(t, config.ClusterConfig{
		Name:    "leak-probe",
		Brokers: []string{unreachableBroker},
		Auth:    sasl,
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "http://192.0.2.1:8081", Username: "sr-user", Password: leakPassword,
		},
	})

	cases := []struct {
		name     string
		method   string
		path     string
		header   string
		timeout  time.Duration
		wantCode int
	}{
		{
			name: "not base64", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header:   "{\"brokers\":[\"203.0.113.10:9092\"],\"auth\":{\"password\":\"" + leakPassword + "\"}}",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "invalid JSON", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header:   base64.StdEncoding.EncodeToString([]byte(`{"auth":{"password":"` + leakPassword + `"`)),
			wantCode: http.StatusBadRequest,
		},
		{
			name: "wrong JSON type", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header:   base64.StdEncoding.EncodeToString([]byte(`{"brokers":"` + leakPassword + `"}`)),
			wantCode: http.StatusBadRequest,
		},
		{
			name: "unsupported auth type", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header: encodeHeader(t, config.ClusterConfig{
				Brokers: []string{"203.0.113.10:9092"},
				Auth:    config.AuthConfig{Type: "kerberos", Username: "u", Password: leakPassword},
			}),
			wantCode: http.StatusBadRequest,
		},
		{
			name: "SSRF-blocked broker", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			header: encodeHeader(t, config.ClusterConfig{
				Brokers: []string{"127.0.0.1:9092"}, Auth: sasl,
			}),
			wantCode: http.StatusBadRequest,
		},
		{
			name: "unreachable brokers: list topics", method: http.MethodGet,
			path: "/api/v1/clusters/__private__/topics", header: unreachable,
			timeout: 500 * time.Millisecond, wantCode: http.StatusBadGateway,
		},
		{
			name: "unreachable brokers: test connection", method: http.MethodPost,
			path: "/api/v1/clusters/_test", header: unreachable, wantCode: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logs := &syncBuffer{}
			logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			reg := kafkapkg.NewRegistry(nil, logger)
			cfg := config.Defaults()
			cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
			h := New(Options{Version: "x", Logger: logger, Registry: reg, Config: cfg})

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(PrivateClusterHeader, tc.header)
			if tc.timeout > 0 {
				ctx, cancel := context.WithTimeout(req.Context(), tc.timeout)
				defer cancel()
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)
			// Closing the registry waits for the kafka client goroutines, so
			// their log lines are in the buffer before it is inspected.
			reg.Close()

			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			body := rec.Body.String()
			logged := logs.String()
			require.NotEmpty(t, logged, "the request log line must have been captured")
			for what, secret := range map[string]string{"password": leakPassword, "raw header": tc.header} {
				assert.NotContains(t, body, secret, "%s leaked into the response body", what)
				assert.NotContains(t, logged, secret, "%s leaked into the logs", what)
			}
			assert.NotContains(t, logged, PrivateClusterHeader+"\":",
				"the header must never be logged as a field")
		})
	}
}

// logRecords parses the JSON log lines in s.
func logRecords(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		out = append(out, rec)
	}
	return out
}

// findLog returns the first record with msg, or fails the test.
func findLog(t *testing.T, recs []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, r := range recs {
		if r["msg"] == msg {
			return r
		}
	}
	require.Failf(t, "log line missing", "no %q in %v", msg, recs)
	return nil
}

// Issue #118 pins the log policy for private clusters: broker and Schema
// Registry host names and resolved IPs MAY appear in operator logs (they
// are no secret and help debugging); credentials and the raw
// X-Kafkito-Cluster header never do. The handler lines name the ad-hoc
// cluster so an operator can correlate them with the request log.
// docs/architecture.md, section "Private clusters", states the same rule.
func TestPrivateClusterLogs_HostsAllowedSecretsNever(t *testing.T) {
	t.Parallel()

	header := encodeHeader(t, config.ClusterConfig{
		Brokers: []string{unreachableBroker},
		Auth:    config.AuthConfig{Type: "plain", Username: "leak-user", Password: leakPassword},
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "http://192.0.2.1:8081", Username: "sr-user", Password: leakPassword,
		},
	})
	cases := []struct {
		name, method, path string
		timeout            time.Duration
		wantCode           int
		wantMsg, wantLevel string
	}{
		{
			name: "test connection", method: http.MethodPost, path: "/api/v1/clusters/_test",
			wantCode: http.StatusOK, wantMsg: "testCluster ping failed", wantLevel: "WARN",
		},
		{
			name: "list topics", method: http.MethodGet, path: "/api/v1/clusters/__private__/topics",
			timeout: 500 * time.Millisecond, wantCode: http.StatusBadGateway,
			wantMsg: "upstream kafka error", wantLevel: "ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logs := &syncBuffer{}
			logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			reg := kafkapkg.NewRegistry(nil, logger)
			cfg := config.Defaults()
			cfg.Server.TestConnectionTimeout = 300 * time.Millisecond
			h := New(Options{Version: "x", Logger: logger, Registry: reg, Config: cfg})

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(PrivateClusterHeader, header)
			if tc.timeout > 0 {
				ctx, cancel := context.WithTimeout(req.Context(), tc.timeout)
				defer cancel()
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			reg.Close()

			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			logged := logs.String()

			line := findLog(t, logRecords(t, logged), tc.wantMsg)
			assert.Equal(t, tc.wantLevel, line["level"])
			assert.Regexp(t, `^__adhoc_[0-9a-f]+$`, line["cluster"], "the handler line names the ad-hoc cluster")

			assert.Contains(t, logged, "192.0.2.1", "broker hosts may be logged (#118)")
			assert.NotContains(t, logged, leakPassword, "credentials are never logged")
			assert.NotContains(t, logged, "leak-user", "SASL user names are never logged")
			assert.NotContains(t, logged, header, "the raw header is never logged")
		})
	}
}
