// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
