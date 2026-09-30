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
	"github.com/FinkeFlo/kafkito/internal/connerr"
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
// X-Kafkito-Cluster header never do. The handler lines name the private
// cluster by its log name (config.ClusterLogName), never by its registry
// name, so an operator can correlate them with the request log.
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
			assert.Regexp(t, `^private-[0-9a-f]{12}$`, line["cluster"], "the handler line names the private cluster by its log name")
			assert.NotContains(t, logged, config.AdhocClusterPrefix, "the registry name is never logged")

			assert.Contains(t, logged, "192.0.2.1", "broker hosts may be logged (#118)")
			assert.NotContains(t, logged, leakPassword, "credentials are never logged")
			assert.NotContains(t, logged, "leak-user", "SASL user names are never logged")
			assert.NotContains(t, logged, header, "the raw header is never logged")
		})
	}
}

// Messages about a private cluster definition, from the X-Kafkito-Cluster
// header, a Test connection body or a copy's dest_cluster_config, are fixed
// texts. They name a broker by its position and never repeat a submitted
// host, URL, auth type, user name or password, a resolved address, resolver
// output or an operating system error.
func TestPrivateClusterMessages_RepeatNoSubmittedValue(t *testing.T) {
	t.Parallel()

	const srUser = "echo-sr-user"
	defs := []struct {
		name   string
		cfg    config.ClusterConfig
		want   string // checked where the request validator does not answer first
		absent []string
	}{
		{
			name: "unresolvable broker", cfg: config.ClusterConfig{Brokers: []string{"echo-marker.invalid:9092"}},
			want:   "broker 1: host name could not be resolved",
			absent: []string{"echo-marker", ".invalid", "lookup", "no such host", ":53", "9092"},
		},
		{
			name: "blocked broker address", cfg: config.ClusterConfig{Brokers: []string{"203.0.113.10:9092", "169.254.169.254:9092"}},
			want:   "broker 2: destination not allowed",
			absent: []string{"169.254", "203.0.113.10", "blocked range"},
		},
		{
			name: "broker resolving to a blocked address", cfg: config.ClusterConfig{Brokers: []string{"localhost:9092"}},
			want:   "broker 1: destination not allowed",
			absent: []string{"localhost", "127.0.0.1", "::1"},
		},
		{
			name: "schema registry URL with credentials", cfg: config.ClusterConfig{
				Brokers:        []string{"203.0.113.10:9092"},
				SchemaRegistry: config.SchemaRegistryConfig{URL: "http://" + srUser + ":" + leakPassword + "@bad host:8081"},
			},
			want:   "schema_registry.url: invalid URL",
			absent: []string{leakPassword, srUser, "bad host", "8081", "http://"},
		},
		{
			name: "SASL without password", cfg: config.ClusterConfig{
				Brokers: []string{"203.0.113.10:9092"},
				Auth:    config.AuthConfig{Type: "scram-sha-512", Username: "echo-sasl-user"},
			},
			want:   "auth.username and auth.password are required for SASL",
			absent: []string{"scram", "echo-sasl-user"},
		},
		{
			name: "unknown auth type", cfg: config.ClusterConfig{
				Brokers: []string{"203.0.113.10:9092"},
				Auth:    config.AuthConfig{Type: "echo-auth-marker", Username: "u", Password: leakPassword},
			},
			absent: []string{"echo-auth-marker", leakPassword},
		},
	}
	source := encodeHeader(t, config.ClusterConfig{Brokers: []string{unreachableBroker}})
	paths := []struct {
		name string
		req  func(cfg config.ClusterConfig) *http.Request
		// validated says whether the request validator sees the definition.
		validated bool
	}{
		{"header", func(cfg config.ClusterConfig) *http.Request {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/__private__/topics", nil)
			req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
			return req
		}, false},
		{"test connection header", func(cfg config.ClusterConfig) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", nil)
			req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
			return req
		}, false},
		{"test connection body", func(cfg config.ClusterConfig) *http.Request {
			body, err := json.Marshal(cfg)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			return req
		}, true},
		{"copy dest_cluster_config", func(cfg config.ClusterConfig) *http.Request {
			body, err := json.Marshal(map[string]any{"dest_topic": "t", "dest_cluster_config": cfg})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/__private__/topics/orders/copy", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(PrivateClusterHeader, source)
			return req
		}, true},
	}
	for _, p := range paths {
		for _, d := range defs {
			t.Run(p.name+"/"+d.name, func(t *testing.T) {
				t.Parallel()

				reg := kafkapkg.NewRegistry(nil, slog.New(slog.DiscardHandler))
				t.Cleanup(reg.Close)
				h := New(Options{Version: "x", Logger: slog.New(slog.DiscardHandler), Registry: reg, Config: config.Defaults()})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, p.req(d.cfg))

				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				body := rec.Body.String()
				if d.want != "" {
					assert.Contains(t, body, d.want)
				} else if !p.validated {
					assert.Contains(t, body, "auth.type not supported")
				}
				for _, s := range d.absent {
					assert.NotContains(t, body, s)
				}
			})
		}
	}
}

// A Test connection that fails to connect reports the class of the
// failure, not the dial error with the address the server tried.
func TestTestCluster_ConnectionFailureNamesNoAddress(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{Brokers: []string{unreachableBroker}}
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	for name, req := range map[string]*http.Request{
		"body":   httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", bytes.NewReader(body)),
		"header": httptest.NewRequest(http.MethodPost, "/api/v1/clusters/_test", nil),
	} {
		if name == "body" {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set(PrivateClusterHeader, encodeHeader(t, cfg))
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := kafkapkg.NewRegistry(nil, slog.New(slog.DiscardHandler))
			t.Cleanup(reg.Close)
			c := config.Defaults()
			c.Server.TestConnectionTimeout = 300 * time.Millisecond
			h := New(Options{Version: "x", Logger: slog.New(slog.DiscardHandler), Registry: reg, Config: c})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var info kafkapkg.ClusterInfo
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
			assert.False(t, info.Reachable)
			assert.Equal(t, connerr.Timeout, info.ErrorClass)
			assert.Equal(t, connerr.Timeout.Message(), info.Error)
			for _, s := range []string{"192.0.2.1", "9092", "dial tcp", "connect:", "i/o timeout", "context deadline"} {
				assert.NotContains(t, rec.Body.String(), s)
			}
		})
	}
}
