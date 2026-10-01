// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FinkeFlo/kafkito/internal/config"
	kafkapkg "github.com/FinkeFlo/kafkito/internal/kafka"
	"github.com/FinkeFlo/kafkito/internal/netguard"
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
		Brokers: []string{"203.0.113.10:9092"},
		Auth:    config.AuthConfig{Type: "none"},
	}

	got, err := decodePrivateClusterHeader(t.Context(), encodeHeader(t, good), config.PrivateClustersConfig{})

	require.NoError(t, err)
	require.Len(t, got.Brokers, 1)
	assert.Equal(t, "203.0.113.10:9092", got.Brokers[0])
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
			raw:  base64.StdEncoding.EncodeToString([]byte(`{"name":"x","brokers":["203.0.113.10:9092"],"auth":{"type":"plain"}}`)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodePrivateClusterHeader(t.Context(), tc.raw, config.PrivateClustersConfig{})

			assert.Error(t, err, "decodePrivateClusterHeader(%q) must reject", tc.name)
		})
	}
}

func TestPrivateClusterMiddleware_StoresDecodedConfigInContext_WhenHeaderValid(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{Name: "x", Brokers: []string{"203.0.113.10:9092"}}
	var seen bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok := privateClusterFromContext(r.Context())
		if !assert.True(t, ok, "ctx must carry decoded config") ||
			!assert.Len(t, got.Brokers, 1) {
			return
		}
		assert.Equal(t, "203.0.113.10:9092", got.Brokers[0])
		seen = true
	})
	h := privateClusterMiddleware(config.PrivateClustersConfig{}, errorWriter{})(next)
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
	h := privateClusterMiddleware(config.PrivateClustersConfig{}, errorWriter{})(next)
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
		assert.True(t, config.IsAdhocClusterName(n1),
			"name = %q, want adhoc prefix %q", n1, config.AdhocClusterPrefix)
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
	cfg := config.ClusterConfig{Brokers: []string{"203.0.113.10:9092"}}
	expected, err := reg.UseAdhoc(cfg)
	require.NoError(t, err, "seed adhoc registration")

	var captured string
	r := chi.NewRouter()
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Group(func(g chi.Router) {
			g.Use(privateClusterMiddleware(config.PrivateClustersConfig{}, errorWriter{}))
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
			g.Use(privateClusterMiddleware(config.PrivateClustersConfig{}, errorWriter{}))
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

func TestValidatePrivateClusterConfig_BlocksSSRFSchemaRegistry(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Brokers: []string{"203.0.113.10:9092"},
		Auth:    config.AuthConfig{Type: "none"},
		SchemaRegistry: config.SchemaRegistryConfig{
			URL: "http://169.254.169.254/latest/meta-data/",
		},
	}

	err := validatePrivateClusterConfig(t.Context(), cfg, config.PrivateClustersConfig{})

	require.EqualError(t, err, "schema_registry.url: destination not allowed",
		"SR URL pointing at the metadata endpoint must be rejected")
}

func TestValidatePrivateClusterConfig_BlocksSSRFBroker(t *testing.T) {
	t.Parallel()

	cfg := config.ClusterConfig{
		Brokers: []string{"203.0.113.10:9092", "127.0.0.1:9092"},
		Auth:    config.AuthConfig{Type: "none"},
	}

	err := validatePrivateClusterConfig(t.Context(), cfg, config.PrivateClustersConfig{})

	require.EqualError(t, err, "broker 2: destination not allowed",
		"a loopback broker must be rejected even after a valid one, named by its position")
}

// A failed lookup is reported as a fixed text: the resolver's error names
// the resolver's own address.
func TestValidateClusterPolicy_LookupErrorIsFixedText(t *testing.T) {
	t.Parallel()

	lookup := func(_ context.Context, host string) ([]string, error) {
		return nil, &net.DNSError{Err: "no such host", Name: host, Server: "192.168.65.7:53", IsNotFound: true}
	}
	cfg := config.ClusterConfig{
		Brokers:        []string{"203.0.113.10:9092"},
		SchemaRegistry: config.SchemaRegistryConfig{URL: "https://echo-sr.example.test"},
	}

	err := validateClusterPolicyWith(t.Context(), cfg, config.PrivateClustersConfig{}, netguard.NewHostValidator(lookup))

	require.EqualError(t, err, "schema_registry.url: host name could not be resolved")
}

// countingLookup resolves every host to a documentation address, except the
// hosts in unresolvable, and counts the lookups per host.
type countingLookup struct {
	unresolvable []string

	mu    sync.Mutex
	calls map[string]int
}

func (c *countingLookup) lookup(_ context.Context, host string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = make(map[string]int)
	}
	c.calls[host]++
	if slices.Contains(c.unresolvable, host) {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []string{"203.0.113.10"}, nil
}

func (c *countingLookup) counts() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.calls)
}

// manyBrokers returns n broker addresses with distinct host names.
func manyBrokers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("echo-broker-%d.example.test:9092", i+1)
	}
	return out
}

// A definition names at most 50 brokers. The count is checked before any
// host is resolved.
func TestValidateClusterPolicy_BrokerCap(t *testing.T) {
	t.Parallel()

	var lookups countingLookup
	err := validateClusterPolicyWith(t.Context(), config.ClusterConfig{Brokers: manyBrokers(maxBrokersPerCluster + 1)}, config.PrivateClustersConfig{}, netguard.NewHostValidator(lookups.lookup))
	require.EqualError(t, err, "too many brokers (max 50)")
	assert.Empty(t, lookups.counts(), "no host is resolved")

	err = validateClusterPolicyWith(t.Context(), config.ClusterConfig{Brokers: manyBrokers(maxBrokersPerCluster)}, config.PrivateClustersConfig{}, netguard.NewHostValidator(lookups.lookup))
	require.NoError(t, err)
	assert.Len(t, lookups.counts(), maxBrokersPerCluster)
}

// The definitions of one request share its HostValidator: each distinct
// host, in any letter case, is resolved once per request.
func TestValidateClusterPolicy_ResolvesEachHostOncePerRequest(t *testing.T) {
	t.Parallel()

	var lookups countingLookup
	var ctxs []context.Context
	h := hostValidatorMiddleware(lookups.lookup)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxs = append(ctxs, r.Context())
	}))
	for range 2 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}
	require.Len(t, ctxs, 2)

	require.NoError(t, validateClusterPolicy(ctxs[0], config.ClusterConfig{
		Brokers:        []string{"a.example.test:9092", "A.example.test:9093", "b.example.test:9092"},
		SchemaRegistry: config.SchemaRegistryConfig{URL: "https://b.example.test:8081"},
	}, config.PrivateClustersConfig{}))
	require.NoError(t, validateClusterPolicy(ctxs[0], config.ClusterConfig{
		Brokers: []string{"b.example.test:9094", "c.example.test:9092", "a.example.test:9092"},
	}, config.PrivateClustersConfig{}))
	assert.Equal(t, map[string]int{"a.example.test": 1, "b.example.test": 1, "c.example.test": 1}, lookups.counts())

	// Another request resolves again.
	require.NoError(t, validateClusterPolicy(ctxs[1], config.ClusterConfig{Brokers: []string{"a.example.test:9092"}}, config.PrivateClustersConfig{}))
	assert.Equal(t, 2, lookups.counts()["a.example.test"])
}

// The X-Kafkito-Cluster header and a Test connection body or a
// dest_cluster_config of the same request resolve each distinct host once.
func TestPrivateClusterRequests_ResolveEachHostOnce(t *testing.T) {
	t.Parallel()

	header := encodeHeader(t, config.ClusterConfig{Brokers: []string{"a.example.test:9092", "b.example.test:9092", "A.example.test:9093"}})
	for _, tc := range []struct {
		name, path, body, want string
	}{
		{
			"test connection", "/api/v1/clusters/_test",
			`{"brokers":["b.example.test:9094","c.example.test:9092"]}`,
			`{"error":"broker 2: host name could not be resolved"}`,
		},
		{
			"copy", "/api/v1/clusters/" + config.PrivateClusterSentinel + "/topics/orders/copy",
			`{"dest_topic":"t","dest_cluster_config":{"brokers":["B.example.test:9094","c.example.test:9092"]}}`,
			`{"error":"dest_cluster_config: broker 2: host name could not be resolved"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookups := &countingLookup{unresolvable: []string{"c.example.test"}}
			st := stores{clusters: fakeClusters{}}
			h := New(Options{
				Version:    "v-test",
				Logger:     slog.New(slog.DiscardHandler),
				Config:     config.Defaults(),
				stores:     &st,
				lookupHost: lookups.lookup,
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(PrivateClusterHeader, header)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.want, rec.Body.String())
			assert.Equal(t, map[string]int{"a.example.test": 1, "b.example.test": 1, "c.example.test": 1}, lookups.counts())
		})
	}
}

// The X-Kafkito-Cluster header is not schema-validated: its broker count is
// checked in Go, before any host is resolved.
func TestPrivateClusterHeader_TooManyBrokers(t *testing.T) {
	t.Parallel()

	lookups := &countingLookup{}
	st := stores{clusters: fakeClusters{}}
	h := New(Options{
		Version:    "v-test",
		Logger:     slog.New(slog.DiscardHandler),
		Config:     config.Defaults(),
		stores:     &st,
		lookupHost: lookups.lookup,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+config.PrivateClusterSentinel+"/topics", nil)
	req.Header.Set(PrivateClusterHeader, encodeHeader(t, config.ClusterConfig{Brokers: manyBrokers(60)}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"error":"X-Kafkito-Cluster: too many brokers (max 50)"}`, rec.Body.String())
	assert.Empty(t, lookups.counts())
}
