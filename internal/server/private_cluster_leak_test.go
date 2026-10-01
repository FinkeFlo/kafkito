// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
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
	gen "github.com/FinkeFlo/kafkito/internal/server/api"
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

// The Schema Registry credentials of credentialedCluster differ from its
// SASL credentials, so a failing check names the one it found.
const (
	leakSRUser     = "sr-canary-user"
	leakSRPassword = "Sr-Secret-Canary-Value"
)

// credentialedCluster is a private cluster definition with SASL/PLAIN over
// TLS and a Schema Registry with basic auth. Neither broker nor registry
// answers.
func credentialedCluster(broker string) config.ClusterConfig {
	return config.ClusterConfig{
		Name:    "canary-probe",
		Brokers: []string{broker},
		Auth:    config.AuthConfig{Type: "plain", Username: "leak-user", Password: leakPassword},
		TLS:     config.TLSConfig{Enabled: true},
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "https://10.255.255.1:1", Username: leakSRUser, Password: leakSRPassword,
		},
	}
}

// assertNoPrivateSecrets fails when the response body or the logs contain
// a credential of credentialedCluster, the X-Kafkito-Cluster header value or
// a registry name. echoed is a registry name the client itself put in the
// path: the 404 repeats it like any unknown cluster name.
func assertNoPrivateSecrets(t *testing.T, body, logs, header, echoed string) {
	t.Helper()
	require.Contains(t, logs, "http request", "the request log line must have been captured")
	for what, s := range map[string]string{
		"SASL user":                "leak-user",
		"SASL password":            leakPassword,
		"Schema Registry user":     leakSRUser,
		"Schema Registry password": leakSRPassword,
		"X-Kafkito-Cluster header": header,
	} {
		if s == "" {
			continue
		}
		assert.NotContains(t, body, s, "%s in the response", what)
		assert.NotContains(t, logs, s, "%s in the logs", what)
	}
	if echoed != "" {
		body = strings.ReplaceAll(body, echoed, "")
	}
	assert.NotContains(t, body, config.AdhocClusterPrefix, "registry name in the response")
	assert.NotContains(t, logs, config.AdhocClusterPrefix, "registry name in the logs")
}

// Private-cluster credentials travel in X-Kafkito-Cluster on every request.
// Neither a password nor the raw header value may show up in a response
// body or in any log line (request log, handler logs, kafka client logs) of
// either log format, and neither may a registry name — for malformed
// headers, SSRF-rejected configs and well-formed configs whose brokers are
// unreachable.
//
// encodeHeader produces the same format as the frontend's
// encodePrivateClusterHeader: base64 of the JSON ClusterConfig.
func TestPrivateClusterHeader_NeverLeaksCredentials(t *testing.T) {
	t.Parallel()

	unreachable := encodeHeader(t, credentialedCluster(unreachableBroker))

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
			header:   encodeHeader(t, credentialedCluster("127.0.0.1:9092")),
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
	headerField := map[string]string{"json": `"` + PrivateClusterHeader + `":`, "text": PrivateClusterHeader + "="}
	for _, format := range privateLogFormats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				logs := &syncBuffer{}
				logger := slog.New(format.handler(logs))
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
				logged := logs.String()
				assertNoPrivateSecrets(t, rec.Body.String(), logged, tc.header, "")
				require.Contains(t, headerField, format.name)
				assert.NotContains(t, logged, headerField[format.name],
					"the header must never be logged as a field")
			})
		}
	}
}

// For a private cluster with SASL and Schema Registry credentials, no
// response and no log line of either log format contains a credential, the
// X-Kafkito-Cluster header value or a registry name: Schema Registry calls,
// a search whose JS filter fails, produce, the delete endpoints, a handler
// panic and a registry name in the path.
func TestPrivateClusterRequests_NoCredentialsInResponsesOrLogs(t *testing.T) {
	t.Parallel()

	const (
		priv = "/api/v1/clusters/__private__"
		// registrySegment in a path stands for the cluster's registry name.
		registrySegment = "{registry}"
		aclFilter       = `{"principal":"User:app","host":"*","resource_type":"TOPIC","resource_name":"orders","pattern_type":"LITERAL","operation":"READ","permission_type":"ALLOW"}`
	)
	cluster := credentialedCluster(unreachableBroker)
	header := encodeHeader(t, cluster)
	cases := []struct {
		name, method, path, body string
		// noHeader sends the request without X-Kafkito-Cluster.
		noHeader bool
		// panics makes every handler panic.
		panics   bool
		wantCode int
		// wantLog is a message the request must have logged.
		wantLog string
	}{
		{
			name: "schema registry: list subjects", method: http.MethodGet, path: priv + "/schemas/subjects",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "schema registry: get schema", method: http.MethodGet, path: priv + "/schemas/subjects/orders-value/versions/latest",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "schema registry: register schema", method: http.MethodPost, path: priv + "/schemas/subjects/orders-value/versions",
			body: `{"schema":"\"string\""}`, wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "search: JS filter does not compile", method: http.MethodPost, path: priv + "/topics/orders/messages/search",
			body: `{"mode":"js","value":"value.includes("}`, wantCode: http.StatusBadRequest, wantLog: "http request",
		},
		{
			name: "search: JS filter throws", method: http.MethodPost, path: priv + "/topics/orders/messages/search",
			body: `{"mode":"js","value":"throw new Error('filter failed'); return true"}`, wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "produce", method: http.MethodPost, path: priv + "/topics/orders/messages",
			body: `{"key":"k","value":"v"}`, wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete topic", method: http.MethodDelete, path: priv + "/topics/orders",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete records", method: http.MethodDelete, path: priv + "/topics/orders/records",
			body: `{"partitions":{"0":-1}}`, wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete group", method: http.MethodDelete, path: priv + "/groups/billing",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete schema subject", method: http.MethodDelete, path: priv + "/schemas/subjects/orders-value",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete ACLs", method: http.MethodDelete, path: priv + "/acls",
			body: aclFilter, wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "delete SCRAM user", method: http.MethodDelete, path: priv + "/users/app?mechanism=SCRAM-SHA-256",
			wantCode: http.StatusBadGateway, wantLog: "upstream kafka error",
		},
		{
			name: "handler panic", method: http.MethodGet, path: priv + "/topics",
			panics: true, wantCode: http.StatusInternalServerError, wantLog: "panic recovered",
		},
		{
			name: "registry name in the path", method: http.MethodGet, path: "/api/v1/clusters/" + registrySegment + "/topics",
			noHeader: true, wantCode: http.StatusNotFound, wantLog: "http request",
		},
		{
			name: "unregistered registry name in the path", method: http.MethodGet, path: "/api/v1/clusters/" + unregisteredAdhocName + "/topics",
			wantCode: http.StatusNotFound, wantLog: "http request",
		},
	}
	for _, format := range privateLogFormats {
		for _, tc := range cases {
			t.Run(format.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				logs := &syncBuffer{}
				logger := slog.New(format.handler(logs))
				reg := kafkapkg.NewRegistry(nil, logger)
				internal, err := reg.UseAdhoc(cluster)
				require.NoError(t, err)
				opts := Options{Version: "x", Logger: logger, Registry: reg, Config: config.Defaults()}
				if tc.panics {
					opts.strictMiddlewares = []gen.StrictMiddlewareFunc{panicInHandler}
				}
				h := New(opts)

				path, echoed := tc.path, ""
				switch {
				case strings.Contains(path, registrySegment):
					path, echoed = strings.Replace(path, registrySegment, internal, 1), internal
				case strings.Contains(path, unregisteredAdhocName):
					echoed = unregisteredAdhocName
				}
				var body io.Reader = http.NoBody
				if tc.body != "" {
					body = strings.NewReader(tc.body)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
				defer cancel()
				req := httptest.NewRequestWithContext(ctx, tc.method, path, body)
				if tc.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				if !tc.noHeader {
					req.Header.Set(PrivateClusterHeader, header)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				reg.Close()

				require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
				logged := logs.String()
				assert.NotEmpty(t, logLinesWith(logged, tc.wantLog), "no %q line in:\n%s", tc.wantLog, logged)
				assertNoPrivateSecrets(t, rec.Body.String(), logged, header, echoed)
			})
		}
	}
}

// A copy from a private source into a dest_cluster_config, both with SASL
// and Schema Registry credentials, that ends when the client goes away:
// neither the stream nor a log line of either log format contains a
// credential, the X-Kafkito-Cluster header value or a registry name.
// Sequential: the job holds a copy slot.
func TestCopyStream_PrivateClustersNoCredentialsInStreamOrLogs(t *testing.T) {
	source := credentialedCluster(unreachableBroker)
	header := encodeHeader(t, source)
	body, err := json.Marshal(map[string]any{
		"dest_cluster_config": credentialedCluster("192.0.2.2:9092"),
		"dest_topic":          "orders2",
		"limit":               1,
	})
	require.NoError(t, err)
	for _, format := range privateLogFormats {
		t.Run(format.name, func(t *testing.T) {
			logs := &syncBuffer{}
			logger := slog.New(format.handler(logs))
			reg := kafkapkg.NewRegistry(nil, logger)
			h := New(Options{Version: "x", Logger: logger, Registry: reg, Config: config.Defaults()})

			// The client goes away while the job waits for the brokers.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := time.AfterFunc(300*time.Millisecond, cancel)
			defer stop.Stop()
			req := newCopyRequest("/api/v1/clusters/__private__/topics/orders/copy", string(body)).WithContext(ctx)
			req.Header.Set(PrivateClusterHeader, header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Eventually(t, func() bool { return len(copySlots) == 0 }, 5*time.Second, 10*time.Millisecond,
				"the copy job releases its slot")
			reg.Close()

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "data: ", "the stream must have been captured")
			logged := logs.String()
			assert.NotEmpty(t, logLinesWith(logged, "copy: destination pre-flight check skipped"), logged)
			assertNoPrivateSecrets(t, rec.Body.String(), logged, header, "")
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
		TLS:     config.TLSConfig{Enabled: true},
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
		want   string
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
		{
			name: "too many brokers", cfg: config.ClusterConfig{Brokers: manyBrokers(60)},
			want:   "too many brokers (max 50)",
			absent: []string{"echo-broker", "example.test", "9092"},
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
				switch {
				case d.want != "":
					assert.Contains(t, body, d.want)
				case !p.validated:
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
