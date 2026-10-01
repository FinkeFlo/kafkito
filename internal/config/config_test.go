// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultsLoad(t *testing.T) {
	isolateEnv(t)

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
	isolateEnv(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(`
server:
  addr: ":9999"
clusters:
  - name: dev
    is_prod: true
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
	assert.True(t, cfg.Clusters[0].IsProd)
	assert.Equal(t, "staging", cfg.Clusters[1].Name)
	assert.False(t, cfg.Clusters[1].IsProd)
}

func TestEnvOverridesYAML(t *testing.T) {
	isolateEnv(t)
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

func TestAuthModeDefaultsToOff_WhenEnvIsEmpty(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, DefaultAuthMode, cfg.Auth.Mode)
	assert.Equal(t, "off", cfg.Auth.Mode)
}

func TestAuthModeDefaultsToOff_WhenUnset(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")
	require.NoError(t, os.Unsetenv("KAFKITO_AUTH_MODE"))

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "off", cfg.Auth.Mode)
}

func TestAuthModeYAMLBinding(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")
	require.NoError(t, os.Unsetenv("KAFKITO_AUTH_MODE"))
	p := writeYAML(t, "auth:\n  mode: mock\n")

	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Auth.Mode)
}

func TestAuthModeDefaultsToOff_WhenYAMLIsEmpty(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "")
	require.NoError(t, os.Unsetenv("KAFKITO_AUTH_MODE"))
	p := writeYAML(t, "auth:\n  mode: \"\"\n")

	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "off", cfg.Auth.Mode)
}

func TestAuthModeEnvOverridesYAML(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_AUTH_MODE", "mock")
	p := writeYAML(t, "auth:\n  mode: \"off\"\n")

	cfg, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Auth.Mode)
}

// TestClusterConfigJSONRoundTrip guards the json tags on ClusterConfig and its
// nested structs. Ad-hoc ("private") clusters reach the server as JSON — via
// the X-Kafkito-Cluster header or a copy request's dest_cluster_config — and
// encoding/json's fallback case-insensitive matching does not ignore
// underscores. Dropping these tags silently discards every multi-word key,
// which is invisible in tests that only assert on single-word fields: it once
// left private clusters unable to reach a Schema Registry and made them bypass
// the is_prod production-confirmation gate. The payload below is the exact
// shape frontend/src/lib/private-clusters.ts sends.
func TestClusterConfigJSONRoundTrip(t *testing.T) {
	const payload = `{
		"name": "priv",
		"is_prod": true,
		"brokers": ["b1:9092"],
		"auth": {"type": "scram-sha-512", "username": "u", "password": "p"},
		"tls": {"enabled": true, "insecure_skip_verify": true},
		"schema_registry": {
			"url": "https://sr:8081",
			"username": "su",
			"password": "sp",
			"insecure_skip_verify": true
		},
		"data_masking": [
			{"topics": ["orders"], "fields": ["$.pan"], "replacement": "###",
			 "regex": [{"match": "\\d{4}", "replacement": "####"}]}
		]
	}`

	var c ClusterConfig
	require.NoError(t, json.Unmarshal([]byte(payload), &c))

	assert.Equal(t, "priv", c.Name)
	assert.True(t, c.IsProd, "is_prod dropped: private prod clusters would skip the confirmation gate")
	assert.Equal(t, []string{"b1:9092"}, c.Brokers)
	assert.Equal(t, AuthConfig{Type: "scram-sha-512", Username: "u", Password: "p"}, c.Auth)
	assert.Equal(t, TLSConfig{Enabled: true, InsecureSkipVerify: true}, c.TLS)
	assert.Equal(t, SchemaRegistryConfig{
		URL:                "https://sr:8081",
		Username:           "su",
		Password:           "sp",
		InsecureSkipVerify: true,
	}, c.SchemaRegistry, "schema_registry dropped: SR decoding would be silently unavailable")
	require.Len(t, c.DataMasking, 1)
	assert.Equal(t, MaskingRule{
		Topics:      []string{"orders"},
		Fields:      []string{"$.pan"},
		Regex:       []RegexMask{{Match: `\d{4}`, Replacement: "####"}},
		Replacement: "###",
	}, c.DataMasking[0])

	// Marshalling must stay symmetric so a decoded config can be re-encoded
	// (e.g. when proxying an ad-hoc config onward) without losing fields.
	out, err := json.Marshal(c)
	require.NoError(t, err)
	var back ClusterConfig
	require.NoError(t, json.Unmarshal(out, &back))
	assert.Equal(t, c, back)
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

func TestLogDefaults(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, slog.LevelInfo, cfg.Log.SlogLevel())
	assert.Equal(t, "json", cfg.Log.FormatName())
}

func TestLogEnvBinding(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_LOG_LEVEL", "DEBUG")
	t.Setenv("KAFKITO_LOG_FORMAT", "text")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, slog.LevelDebug, cfg.Log.SlogLevel())
	assert.Equal(t, "text", cfg.Log.FormatName())
}

func TestLogEmptyEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_LOG_LEVEL", "")
	t.Setenv("KAFKITO_LOG_FORMAT", "")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, slog.LevelInfo, cfg.Log.SlogLevel())
	assert.Equal(t, "json", cfg.Log.FormatName())
}

func TestLogValidation(t *testing.T) {
	cases := []struct {
		name    string
		log     LogConfig
		wantErr string
	}{
		{"all levels ok", LogConfig{Level: "warn", Format: "json"}, ""},
		{"error level ok", LogConfig{Level: "error", Format: "text"}, ""},
		{"unknown level", LogConfig{Level: "verbose"}, "log.level"},
		{"unknown format", LogConfig{Format: "logfmt"}, "log.format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Config{Log: tc.log}.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLogInvalidEnvRejectedOnLoad(t *testing.T) {
	t.Setenv("KAFKITO_CONFIG", "")
	t.Setenv("KAFKITO_KAFKA_BROKERS", "")
	t.Setenv("KAFKITO_LOG_LEVEL", "trace")

	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.level")
}

// isolateEnv unsets every variable Load consults so tests start from the
// built-in defaults regardless of the developer's shell (.env.dev sets PORT
// and KAFKITO_KAFKA_BROKERS). Unset, not empty: an empty KAFKITO_* variable
// still overrides the YAML file.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KAFKITO_CONFIG", "KAFKITO_KAFKA_BROKERS", "PORT", "KAFKITO_SERVER_ADDR",
		"KAFKITO_TEST_CONNECTION_TIMEOUT", "KAFKITO_SERVER_FRAME_ANCESTORS",
		"KAFKITO_AUTH_MODE", "KAFKITO_AUTH_OIDC_ISSUER_URL", "KAFKITO_AUTH_OIDC_AUDIENCE",
		"KAFKITO_AUTH_OIDC_JWKS_URL", "KAFKITO_AUTH_OIDC_REQUIRED_TYP", "KAFKITO_AUTH_OIDC_ALLOWED_AZP",
		"KAFKITO_PRIVATE_CLUSTERS", "KAFKITO_PRIVATE_CLUSTERS_ALLOW_PLAIN_WITHOUT_TLS",
	} {
		t.Setenv(k, "") // registers the restore
		require.NoError(t, os.Unsetenv(k))
	}
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestServerDefaults(t *testing.T) {
	isolateEnv(t)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, ":37421", cfg.Server.Addr)
	assert.Equal(t, 15*time.Second, cfg.Server.TestConnectionTimeout)
	assert.Equal(t, "'none'", cfg.Server.FrameAncestors)
}

func TestListenAddressPrecedence(t *testing.T) {
	yamlAddr := "server:\n  addr: \"127.0.0.1:1111\"\n"
	cases := []struct {
		name    string
		yaml    string
		envAddr string
		port    string
		want    string
	}{
		{name: "default", want: ":37421"},
		{name: "yaml", yaml: yamlAddr, want: "127.0.0.1:1111"},
		{name: "env beats yaml", yaml: yamlAddr, envAddr: "127.0.0.1:2222", want: "127.0.0.1:2222"},
		{name: "PORT beats default", port: "3333", want: ":3333"},
		{name: "PORT beats yaml", yaml: yamlAddr, port: "3333", want: ":3333"},
		{name: "PORT beats env", yaml: yamlAddr, envAddr: "127.0.0.1:2222", port: "3333", want: ":3333"},
		{name: "PORT=0 picks a free port", port: "0", want: ":0"},
		{name: "named PORT accepted like net.Listen", port: "http", want: ":http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.envAddr != "" {
				t.Setenv("KAFKITO_SERVER_ADDR", tc.envAddr)
			}
			if tc.port != "" {
				t.Setenv("PORT", tc.port)
			}
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Server.Addr)
		})
	}
}

func TestEmptyServerAddrFallsBackToDefault(t *testing.T) {
	isolateEnv(t)
	// Set but empty overrides the YAML value in koanf; the effective
	// address is then the default, as before.
	t.Setenv("KAFKITO_SERVER_ADDR", "")

	cfg, err := Load(writeYAML(t, "server:\n  addr: \"127.0.0.1:1111\"\n"))
	require.NoError(t, err)
	assert.Equal(t, ":37421", cfg.Server.Addr)
}

func TestEmptyPortIgnored(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PORT", "")
	t.Setenv("KAFKITO_SERVER_ADDR", "127.0.0.1:2222")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:2222", cfg.Server.Addr)
}

func TestPortPrefixedVariablesIgnored(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PORTAL_URL", "https://example.com")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, ":37421", cfg.Server.Addr)
}

func TestInvalidListenAddressRejected(t *testing.T) {
	cases := []struct {
		name, port, envAddr string
	}{
		{name: "PORT not a port", port: "abc"},
		{name: "PORT out of range", port: "65536"},
		{name: "PORT with colon", port: "80:90"},
		{name: "addr without port", envAddr: "localhost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.port != "" {
				t.Setenv("PORT", tc.port)
			}
			if tc.envAddr != "" {
				t.Setenv("KAFKITO_SERVER_ADDR", tc.envAddr)
			}

			_, err := Load("")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "server.addr")
		})
	}
}

func TestTestConnectionTimeout(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		env  string
		want time.Duration
	}{
		{name: "env", env: "30s", want: 30 * time.Second},
		{name: "env trimmed", env: " 2m ", want: 2 * time.Minute},
		{name: "empty env keeps default", env: "", want: 15 * time.Second},
		{name: "zero means default", env: "0s", want: 15 * time.Second},
		{name: "yaml", yaml: "server:\n  test_connection_timeout: 45s\n", want: 45 * time.Second},
		{name: "env beats yaml", yaml: "server:\n  test_connection_timeout: 45s\n", env: "5s", want: 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("KAFKITO_TEST_CONNECTION_TIMEOUT", tc.env)
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Server.TestConnectionTimeout)
		})
	}
}

func TestInvalidTestConnectionTimeoutRejected(t *testing.T) {
	for _, v := range []string{"abc", "30", "-5s"} {
		t.Run(v, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("KAFKITO_TEST_CONNECTION_TIMEOUT", v)

			_, err := Load("")
			require.Error(t, err)
		})
	}
}

func TestFrameAncestors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		env  string
		want string
	}{
		{name: "empty env keeps default", want: "'none'"},
		{name: "env", env: "'self' https://*.launchpad.example.com", want: "'self' https://*.launchpad.example.com"},
		{name: "whitespace normalised", env: "  'self'   https://a.example  ", want: "'self' https://a.example"},
		{name: "yaml", yaml: "server:\n  frame_ancestors: \"'self'\"\n", want: "'self'"},
		{name: "env beats yaml", yaml: "server:\n  frame_ancestors: \"'self'\"\n", env: "https://b.example", want: "https://b.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("KAFKITO_SERVER_FRAME_ANCESTORS", tc.env)
			path := ""
			if tc.yaml != "" {
				path = writeYAML(t, tc.yaml)
			}

			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Server.FrameAncestors)
		})
	}
}

func TestFrameAncestorsDirectiveInjectionRejected(t *testing.T) {
	for _, v := range []string{"'self'; script-src *", "'self', https://evil.example"} {
		t.Run(v, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("KAFKITO_SERVER_FRAME_ANCESTORS", v)

			_, err := Load("")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "server.frame_ancestors")
		})
	}
}

func TestBrokersShortcutIsEnvOnly(t *testing.T) {
	isolateEnv(t)
	path := writeYAML(t, "kafka:\n  brokers: \"yaml-host:9092\"\n")

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Clusters, "kafka.brokers in YAML must not synthesize a cluster")
}

func TestBrokersShortcutIgnoredWhenClustersConfigured(t *testing.T) {
	isolateEnv(t)
	t.Setenv("KAFKITO_KAFKA_BROKERS", "env-host:9092")
	path := writeYAML(t, "clusters:\n  - name: dev\n    brokers: [\"yaml-host:9092\"]\n")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Clusters, 1)
	assert.Equal(t, "dev", cfg.Clusters[0].Name)
}

func TestBrokersShortcutTrimsAndDropsEmpty(t *testing.T) {
	isolateEnv(t)
	t.Setenv("KAFKITO_KAFKA_BROKERS", " a:9092 ,, b:9092 ,")

	cfg, err := Load("")
	require.NoError(t, err)
	require.Len(t, cfg.Clusters, 1)
	assert.Equal(t, []string{"a:9092", "b:9092"}, cfg.Clusters[0].Brokers)

	t.Setenv("KAFKITO_KAFKA_BROKERS", " , ")
	cfg, err = Load("")
	require.NoError(t, err)
	assert.Empty(t, cfg.Clusters)
}

func TestAuthOIDCEnvBinding(t *testing.T) {
	isolateEnv(t)
	t.Setenv("KAFKITO_AUTH_MODE", "oidc")
	t.Setenv("KAFKITO_AUTH_OIDC_ISSUER_URL", "https://idp.example.com/realms/kafkito")
	t.Setenv("KAFKITO_AUTH_OIDC_AUDIENCE", "kafkito-api")
	t.Setenv("KAFKITO_AUTH_OIDC_JWKS_URL", " https://idp.example.com/realms/kafkito/certs ")
	t.Setenv("KAFKITO_AUTH_OIDC_REQUIRED_TYP", "at+jwt")
	t.Setenv("KAFKITO_AUTH_OIDC_ALLOWED_AZP", "proxy, cli ,,")

	cfg, err := Load("")

	require.NoError(t, err)
	assert.Equal(t, "oidc", cfg.Auth.Mode)
	assert.Equal(t, OIDCAuthConfig{
		IssuerURL:   "https://idp.example.com/realms/kafkito",
		Audience:    "kafkito-api",
		JWKSURL:     "https://idp.example.com/realms/kafkito/certs",
		RequiredTyp: "at+jwt",
		AllowedAZP:  []string{"proxy", "cli"},
	}, cfg.Auth.OIDC)
}

func TestAuthOIDCYAMLBindingAndEnvOverride(t *testing.T) {
	isolateEnv(t)
	p := writeYAML(t, `
auth:
  mode: oidc
  oidc:
    issuer_url: https://yaml.example.com
    audience: yaml-aud
    required_typ: at+jwt
    allowed_azp: [proxy]
`)
	t.Setenv("KAFKITO_AUTH_OIDC_ISSUER_URL", "https://env.example.com")
	t.Setenv("KAFKITO_AUTH_OIDC_JWKS_URL", "") // empty: keeps the YAML value (none)

	cfg, err := Load(p)

	require.NoError(t, err)
	assert.Equal(t, OIDCAuthConfig{
		IssuerURL:   "https://env.example.com",
		Audience:    "yaml-aud",
		RequiredTyp: "at+jwt",
		AllowedAZP:  []string{"proxy"},
	}, cfg.Auth.OIDC)
}

func TestAuthOIDCMissingIssuerRejectedOnLoad(t *testing.T) {
	isolateEnv(t)
	t.Setenv("KAFKITO_AUTH_MODE", "oidc")
	t.Setenv("KAFKITO_AUTH_OIDC_AUDIENCE", "kafkito-api")

	_, err := Load("")

	require.ErrorContains(t, err, "auth.oidc.issuer_url is required")
}

func TestAuthOIDCValidation(t *testing.T) {
	ok := OIDCAuthConfig{IssuerURL: "https://idp.example.com", Audience: "aud"}
	with := func(f func(*OIDCAuthConfig)) OIDCAuthConfig {
		c := ok
		f(&c)
		return c
	}
	cases := []struct {
		name    string
		mode    string
		oidc    OIDCAuthConfig
		wantErr string
	}{
		{"valid", "oidc", ok, ""},
		{"valid_with_jwks", "oidc", with(func(c *OIDCAuthConfig) { c.JWKSURL = "https://idp.example.com/certs" }), ""},
		{"http_localhost_ok", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://localhost:8080/realms/k" }), ""},
		{"http_loopback_ok", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://127.0.0.1:8080" }), ""},
		{"http_ipv6_loopback_ok", "oidc", with(func(c *OIDCAuthConfig) { c.JWKSURL = "http://[::1]:8080/certs" }), ""},
		{"other_mode_ignores_oidc", "mock", OIDCAuthConfig{IssuerURL: "not a url"}, ""},
		{"missing_issuer", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "" }), "auth.oidc.issuer_url is required"},
		{"missing_audience", "oidc", with(func(c *OIDCAuthConfig) { c.Audience = " " }), "auth.oidc.audience is required"},
		{"relative_issuer", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "idp.example.com" }), "absolute"},
		{"http_remote_issuer", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://idp.example.com" }), "https"},
		{"ftp_issuer", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "ftp://idp.example.com" }), "https"},
		{"userinfo_issuer", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "https://" + testUserInfo + "@idp.example.com" }), "user info"},
		{"http_remote_jwks", "oidc", with(func(c *OIDCAuthConfig) { c.JWKSURL = "http://idp.example.com/certs" }), "auth.oidc.jwks_url"},
		{"issuer_query", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "https://idp.example.com/?x=1" }), "query or fragment"},
		{"issuer_fragment", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "https://idp.example.com/#f" }), "query or fragment"},
		{"unparsable_userinfo", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "https://" + testUserInfoSecret + "@idp.example.com:bad" }), "absolute URL"},
		{"userinfo_redacted", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://" + testUserInfoSecret + "@idp.example.com" }), "user info"},
		{"localhost_dot", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://localhost./x" }), "https"},
		{"ipv6_zone", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://[::1%25lo0]/x" }), "https"},
		{"short_ipv4", "oidc", with(func(c *OIDCAuthConfig) { c.IssuerURL = "http://127.1/x" }), "https"},
		{"empty_azp_entry", "oidc", with(func(c *OIDCAuthConfig) { c.AllowedAZP = []string{"a", " "} }), "auth.oidc.allowed_azp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Config{Auth: AppAuthConfig{Mode: tc.mode, OIDC: tc.oidc}}.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			assert.NotContains(t, err.Error(), "secret", "error must not echo user info")
		})
	}
}

func TestAuthOIDCValuesAreTrimmed(t *testing.T) {
	isolateEnv(t)
	p := writeYAML(t, `
auth:
  mode: oidc
  oidc:
    issuer_url: " https://idp.example.com "
    audience: " kafkito-api "
    required_typ: " at+jwt "
    allowed_azp: [" proxy "]
`)

	cfg, err := Load(p)

	require.NoError(t, err)
	assert.Equal(t, OIDCAuthConfig{
		IssuerURL:   "https://idp.example.com",
		Audience:    "kafkito-api",
		RequiredTyp: "at+jwt",
		AllowedAZP:  []string{"proxy"},
	}, cfg.Auth.OIDC)
}

func TestAuthOIDCEmptyAudienceEnvKeepsYAMLValue(t *testing.T) {
	isolateEnv(t)
	p := writeYAML(t, "auth:\n  mode: oidc\n  oidc:\n    issuer_url: https://idp.example.com\n    audience: yaml-aud\n")
	t.Setenv("KAFKITO_AUTH_OIDC_AUDIENCE", " ")

	cfg, err := Load(p)

	require.NoError(t, err)
	assert.Equal(t, "yaml-aud", cfg.Auth.OIDC.Audience)
}

// Test user info for URLs, kept out of URL literals so secret scanners do not
// flag the fixtures.
const (
	testUserInfo       = "u" + ":p"
	testUserInfoSecret = "u" + ":secret"
)
