// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package config loads kafkito runtime configuration from a YAML file
// (optional) and environment variables (prefix KAFKITO_).
//
// Resolution order (later wins):
//  1. Built-in defaults
//  2. YAML file (if --config is given or KAFKITO_CONFIG is set)
//  3. Environment variables with the KAFKITO_ prefix
//  4. $PORT (Cloud Foundry / Heroku style): when set and non-empty it
//     overrides server.addr with ":$PORT", regardless of where server.addr
//     came from.
//  5. A convenience shortcut: if no clusters are defined but
//     KAFKITO_KAFKA_BROKERS is set, a single cluster named "local"
//     is synthesized from that comma-separated list.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// DefaultIdentityHeader is the HTTP header used to carry the request principal
// when no custom header is configured under rbac.identity.header.
const DefaultIdentityHeader = "X-Kafkito-User"

// PrivateClusterSentinel is the URL cluster-name segment reserved for
// per-request private clusters. When this value appears in the URL path,
// the request must also carry a valid X-Kafkito-Cluster header; the cluster
// configuration is read from that header rather than the static config.
const PrivateClusterSentinel = "__private__"

// AdhocClusterPrefix is the internal cluster-name prefix used by the kafka
// package to register ephemeral (header-provided) cluster configurations.
// Duplicated here so config.Validate can reject shared-cluster names that
// would collide.
const AdhocClusterPrefix = "__adhoc_"

// Config is the root configuration struct.
type Config struct {
	Server   ServerConfig    `koanf:"server"`
	Clusters []ClusterConfig `koanf:"clusters"`
	RBAC     RBACConfig      `koanf:"rbac"`
	Auth     AppAuthConfig   `koanf:"auth"`
	Log      LogConfig       `koanf:"log"`
}

// Log levels and formats accepted by LogConfig.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"

	LogFormatJSON = "json"
	LogFormatText = "text"
)

// LogConfig controls the process logger. Populated from KAFKITO_LOG_LEVEL and
// KAFKITO_LOG_FORMAT (or log.level / log.format in YAML). Values are
// case-insensitive; empty means the default (info, json).
type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

// SlogLevel returns the slog level for Level. Unknown values map to info;
// Validate rejects them before this is reached in normal startup.
func (l LogConfig) SlogLevel() slog.Level {
	switch strings.ToLower(strings.TrimSpace(l.Level)) {
	case LogLevelDebug:
		return slog.LevelDebug
	case LogLevelWarn:
		return slog.LevelWarn
	case LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// FormatName returns the normalized output format ("json" or "text").
func (l LogConfig) FormatName() string {
	if strings.ToLower(strings.TrimSpace(l.Format)) == LogFormatText {
		return LogFormatText
	}
	return LogFormatJSON
}

func (l LogConfig) validate() error {
	switch strings.ToLower(strings.TrimSpace(l.Level)) {
	case "", LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
	default:
		return fmt.Errorf("log.level %q not supported (use debug|info|warn|error)", l.Level)
	}
	switch strings.ToLower(strings.TrimSpace(l.Format)) {
	case "", LogFormatJSON, LogFormatText:
	default:
		return fmt.Errorf("log.format %q not supported (use json|text)", l.Format)
	}
	return nil
}

// AppAuthConfig is the top-level authentication configuration for kafkito itself
// (distinct from per-cluster SASL auth). Mode is populated from KAFKITO_AUTH_MODE.
// Valid values: "off", "mock", "oidc". Tagged builds may register additional
// IdP-specific modes — see internal/auth.Register and the build-tagged files
// under internal/auth/.
type AppAuthConfig struct {
	Mode string `koanf:"mode"`
}

// RBACConfig is the top-level RBAC configuration block.
type RBACConfig struct {
	Enabled     bool            `koanf:"enabled"`
	DefaultRole string          `koanf:"default_role"`
	Identity    IdentityConfig  `koanf:"identity"`
	Roles       []RoleConfig    `koanf:"roles"`
	Subjects    []SubjectConfig `koanf:"subjects"`
}

// IdentityConfig controls how the request principal is resolved.
type IdentityConfig struct {
	Header        string `koanf:"header"`
	AnonymousRole string `koanf:"anonymous_role"`
}

// RoleConfig defines a named role with its permissions.
type RoleConfig struct {
	Name        string             `koanf:"name"`
	Permissions []PermissionConfig `koanf:"permissions"`
}

// PermissionConfig binds a resource glob to a set of actions.
type PermissionConfig struct {
	Resource string   `koanf:"resource"`
	Actions  []string `koanf:"actions"`
}

// SubjectConfig maps a user identity to one or more roles.
type SubjectConfig struct {
	User  string   `koanf:"user"`
	Roles []string `koanf:"roles"`
}

// DefaultAddr is the listen address used when neither server.addr nor $PORT
// is set.
const DefaultAddr = ":37421"

// DefaultTestConnectionTimeout is the budget of the user-driven "Test
// connection" probe when server.test_connection_timeout is unset or zero.
const DefaultTestConnectionTimeout = 15 * time.Second

// DefaultFrameAncestors is the CSP frame-ancestors source list used when
// server.frame_ancestors is unset: the UI must not be framed.
const DefaultFrameAncestors = "'none'"

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	// Addr is the bind address, e.g. ":37421". Empty means DefaultAddr.
	// A non-empty $PORT always wins over this (Load rewrites Addr to
	// ":$PORT"), to stay Cloud-Foundry friendly.
	Addr string `koanf:"addr"`
	// TestConnectionTimeout bounds the "Test connection" probe of
	// POST /api/v1/clusters/_test. Env: KAFKITO_TEST_CONNECTION_TIMEOUT
	// (Go duration, e.g. "30s"). Zero means DefaultTestConnectionTimeout.
	TestConnectionTimeout time.Duration `koanf:"test_connection_timeout"`
	// FrameAncestors is the source list of the Content-Security-Policy
	// frame-ancestors directive, i.e. which origins may embed the UI in a
	// frame. Env: KAFKITO_SERVER_FRAME_ANCESTORS. Empty means
	// DefaultFrameAncestors ('none').
	FrameAncestors string `koanf:"frame_ancestors"`
}

// validate rejects values the server could not use. Empty values are valid
// (they mean "default"). The address check mirrors what net.Listen accepts,
// so a bad $PORT fails at startup instead of when binding.
func (s ServerConfig) validate() error {
	if s.Addr != "" {
		_, port, err := net.SplitHostPort(s.Addr)
		if err != nil {
			return fmt.Errorf("server.addr %q: %w", s.Addr, err)
		}
		if _, err := net.LookupPort("tcp", port); err != nil {
			return fmt.Errorf("server.addr %q: %w", s.Addr, err)
		}
	}
	if s.TestConnectionTimeout < 0 {
		return fmt.Errorf("server.test_connection_timeout %s must not be negative", s.TestConnectionTimeout)
	}
	if strings.ContainsAny(s.FrameAncestors, ";,\r\n") {
		return fmt.Errorf("server.frame_ancestors must be a space-separated CSP source list (no ';', ',' or newlines)")
	}
	return nil
}

// ClusterConfig describes one Kafka cluster kafkito can connect to.
//
// The json tags are load-bearing, not decoration: ad-hoc ("private") clusters
// arrive as JSON — base64 in the X-Kafkito-Cluster header, or inline as a
// copy request's dest_cluster_config — and encoding/json's fallback
// case-insensitive field matching does NOT ignore underscores. Without a json
// tag every multi-word key (is_prod, schema_registry, insecure_skip_verify,
// data_masking) is silently dropped, which previously left private clusters
// unable to use a Schema Registry and made them invisible to the is_prod
// production-confirmation gate.
type ClusterConfig struct {
	Name           string               `koanf:"name" json:"name"`
	IsProd         bool                 `koanf:"is_prod" json:"is_prod"`
	Brokers        []string             `koanf:"brokers" json:"brokers"`
	Auth           AuthConfig           `koanf:"auth" json:"auth"`
	TLS            TLSConfig            `koanf:"tls" json:"tls"`
	SchemaRegistry SchemaRegistryConfig `koanf:"schema_registry" json:"schema_registry"`
	DataMasking    []MaskingRule        `koanf:"data_masking" json:"data_masking"`
}

// MaskingRule applies masking to a subset of topics on the cluster. At least
// one of Fields or Regex must be populated. Topics is a list of topic name
// patterns (Go regex); the rule triggers when any pattern matches the topic.
// If Topics is empty, the rule matches all topics.
type MaskingRule struct {
	Topics      []string    `koanf:"topics" json:"topics"`
	Fields      []string    `koanf:"fields" json:"fields"`           // JSONPath expressions
	Regex       []RegexMask `koanf:"regex" json:"regex"`             // regex-based replacements applied on the raw string
	Replacement string      `koanf:"replacement" json:"replacement"` // default replacement for Fields; empty = "***"
}

// RegexMask describes a single regex substitution.
type RegexMask struct {
	Match       string `koanf:"match" json:"match"`
	Replacement string `koanf:"replacement" json:"replacement"`
}

// SchemaRegistryConfig is an optional per-cluster Confluent-/Apicurio-compatible
// Schema Registry endpoint. When URL is empty, SR features are disabled for that cluster.
type SchemaRegistryConfig struct {
	URL                string `koanf:"url" json:"url"`
	Username           string `koanf:"username" json:"username"`
	Password           string `koanf:"password" json:"password"`
	InsecureSkipVerify bool   `koanf:"insecure_skip_verify" json:"insecure_skip_verify"`
}

// AuthConfig is per-cluster authentication. Type "" or "none" disables SASL.
// Valid types: "none", "plain", "scram-sha-256", "scram-sha-512".
type AuthConfig struct {
	Type     string `koanf:"type" json:"type"`
	Username string `koanf:"username" json:"username"`
	Password string `koanf:"password" json:"password"`
}

// TLSConfig enables TLS for the cluster connection. When Enabled is true,
// franz-go uses the system trust store unless InsecureSkipVerify is set.
type TLSConfig struct {
	Enabled            bool `koanf:"enabled" json:"enabled"`
	InsecureSkipVerify bool `koanf:"insecure_skip_verify" json:"insecure_skip_verify"`
}

// Redacted returns a copy of the cluster config safe to log or expose via
// API. Secrets (password) are replaced with "***" if non-empty.
func (c ClusterConfig) Redacted() ClusterConfig {
	r := c
	if r.Auth.Password != "" {
		r.Auth.Password = "***"
	}
	if r.SchemaRegistry.Password != "" {
		r.SchemaRegistry.Password = "***"
	}
	return r
}

// Defaults returns the built-in default configuration.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Addr:                  DefaultAddr,
			TestConnectionTimeout: DefaultTestConnectionTimeout,
			FrameAncestors:        DefaultFrameAncestors,
		},
		Log: LogConfig{Level: LogLevelInfo, Format: LogFormatJSON},
	}
}

// Load merges defaults, an optional YAML file and KAFKITO_* env vars.
// path may be empty; if empty, KAFKITO_CONFIG is consulted.
func Load(path string) (Config, error) {
	k := koanf.New(".")

	cfg := Defaults()
	if err := k.Load(structs.Provider(cfg, "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	if path == "" {
		path = os.Getenv("KAFKITO_CONFIG")
	}
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("load config file %q: %w", path, err)
		}
	}

	// Env is loaded into its own instance first so env-only shortcuts
	// (KAFKITO_KAFKA_BROKERS) cannot be triggered from the YAML file.
	envK := koanf.New(".")
	if err := envK.Load(env.ProviderWithValue("KAFKITO_", ".", envKeyValue), nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}
	if err := envK.Load(env.ProviderWithValue("PORT", ".", portEnvKeyValue), nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}
	if err := k.Merge(envK); err != nil {
		return Config{}, fmt.Errorf("merge env: %w", err)
	}

	var out Config
	if err := k.Unmarshal("", &out); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}

	applyServerDefaults(&out.Server)
	applyShortcuts(&out, envK.String(kafkaBrokersKey))

	if err := out.Validate(); err != nil {
		return Config{}, err
	}
	return out, nil
}

// Validate returns an error for structurally invalid configurations.
// It is intentionally lenient: zero clusters is allowed (kafkito still
// starts, but cluster-scoped endpoints will report unavailable).
func (c Config) Validate() error {
	if err := c.Server.validate(); err != nil {
		return err
	}
	if err := c.Log.validate(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(c.Clusters))
	for i, cl := range c.Clusters {
		if strings.TrimSpace(cl.Name) == "" {
			return fmt.Errorf("clusters[%d]: name is required", i)
		}
		if cl.Name == PrivateClusterSentinel ||
			strings.HasPrefix(cl.Name, AdhocClusterPrefix) {
			return fmt.Errorf("clusters[%d] (%s): name is reserved for private clusters", i, cl.Name)
		}
		if _, dup := seen[cl.Name]; dup {
			return fmt.Errorf("clusters[%d]: duplicate name %q", i, cl.Name)
		}
		seen[cl.Name] = struct{}{}
		if len(cl.Brokers) == 0 {
			return fmt.Errorf("clusters[%d] (%s): at least one broker is required", i, cl.Name)
		}
		if err := validateAuth(cl.Auth); err != nil {
			return fmt.Errorf("clusters[%d] (%s): %w", i, cl.Name, err)
		}
	}
	return nil
}

func validateAuth(a AuthConfig) error {
	t := strings.ToLower(strings.TrimSpace(a.Type))
	switch t {
	case "", "none":
		return nil
	case "plain", "scram-sha-256", "scram-sha-512":
		if a.Username == "" || a.Password == "" {
			return fmt.Errorf("auth %q requires username and password", t)
		}
		return nil
	default:
		return fmt.Errorf("auth.type %q not supported (use none|plain|scram-sha-256|scram-sha-512)", a.Type)
	}
}

// ClusterByName returns the cluster with the given name, or false.
func (c Config) ClusterByName(name string) (ClusterConfig, bool) {
	for _, cl := range c.Clusters {
		if cl.Name == name {
			return cl, true
		}
	}
	return ClusterConfig{}, false
}

// kafkaBrokersKey is the koanf key KAFKITO_KAFKA_BROKERS maps to. It is not
// part of Config; applyShortcuts consumes it.
const kafkaBrokersKey = "kafka.brokers"

// envKeyAliases maps env vars whose koanf key contains an underscore, which
// the generic "_" -> "." transform cannot express.
var envKeyAliases = map[string]string{
	"KAFKITO_TEST_CONNECTION_TIMEOUT": "server.test_connection_timeout",
	"KAFKITO_SERVER_FRAME_ANCESTORS":  "server.frame_ancestors",
}

// envKeyValue maps KAFKITO_* env vars to koanf keys: aliases first, then
// e.g. KAFKITO_SERVER_ADDR -> server.addr and KAFKITO_LOG_LEVEL -> log.level.
// Aliased values are trimmed and dropped when empty so that an empty
// variable keeps the built-in default.
func envKeyValue(key, value string) (string, any) {
	if alias, ok := envKeyAliases[key]; ok {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", nil
		}
		return alias, value
	}
	return envKeyTransform(key), value
}

// envKeyTransform maps e.g. KAFKITO_SERVER_ADDR -> server.addr and
// KAFKITO_LOG_LEVEL -> log.level.
func envKeyTransform(key string) string {
	key = strings.ToLower(strings.TrimPrefix(key, "KAFKITO_"))
	return strings.ReplaceAll(key, "_", ".")
}

// portEnvKeyValue maps a non-empty $PORT to server.addr=":$PORT". Other
// variables sharing the "PORT" prefix are ignored.
func portEnvKeyValue(key, value string) (string, any) {
	if key != "PORT" || value == "" {
		return "", nil
	}
	return "server.addr", ":" + value
}

// applyServerDefaults restores defaults for server settings explicitly set
// to their zero value (e.g. KAFKITO_SERVER_ADDR="").
func applyServerDefaults(s *ServerConfig) {
	if s.Addr == "" {
		s.Addr = DefaultAddr
	}
	if s.TestConnectionTimeout == 0 {
		s.TestConnectionTimeout = DefaultTestConnectionTimeout
	}
	if strings.TrimSpace(s.FrameAncestors) == "" {
		s.FrameAncestors = DefaultFrameAncestors
	}
	s.FrameAncestors = strings.Join(strings.Fields(s.FrameAncestors), " ")
}

// applyShortcuts synthesizes a default cluster from the KAFKITO_KAFKA_BROKERS
// value (rawBrokers) when no clusters are otherwise configured.
func applyShortcuts(c *Config, rawBrokers string) {
	if len(c.Clusters) > 0 {
		return
	}
	brokers := splitCSV(rawBrokers)
	if len(brokers) == 0 {
		return
	}
	c.Clusters = []ClusterConfig{{Name: "local", Brokers: brokers}}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ErrNoSuchCluster is returned by lookups on an unknown cluster name.
var ErrNoSuchCluster = errors.New("cluster not configured")
