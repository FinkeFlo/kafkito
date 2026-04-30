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
//  4. A convenience shortcut: if no clusters are defined but
//     KAFKITO_KAFKA_BROKERS is set, a single cluster named "local"
//     is synthesized from that comma-separated list.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

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

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	// Addr is the bind address, e.g. ":37421". Empty means the default.
	// $PORT always wins over this, to stay Cloud-Foundry friendly.
	Addr string `koanf:"addr"`
}

// ClusterConfig describes one Kafka cluster kafkito can connect to.
type ClusterConfig struct {
	Name           string               `koanf:"name"`
	Brokers        []string             `koanf:"brokers"`
	Auth           AuthConfig           `koanf:"auth"`
	TLS            TLSConfig            `koanf:"tls"`
	SchemaRegistry SchemaRegistryConfig `koanf:"schema_registry"`
	DataMasking    []MaskingRule        `koanf:"data_masking"`
}

// MaskingRule applies masking to a subset of topics on the cluster. At least
// one of Fields or Regex must be populated. Topics is a list of topic name
// patterns (Go regex); the rule triggers when any pattern matches the topic.
// If Topics is empty, the rule matches all topics.
type MaskingRule struct {
	Topics      []string    `koanf:"topics"`
	Fields      []string    `koanf:"fields"`      // JSONPath expressions
	Regex       []RegexMask `koanf:"regex"`       // regex-based replacements applied on the raw string
	Replacement string      `koanf:"replacement"` // default replacement for Fields; empty = "***"
}

// RegexMask describes a single regex substitution.
type RegexMask struct {
	Match       string `koanf:"match"`
	Replacement string `koanf:"replacement"`
}

// SchemaRegistryConfig is an optional per-cluster Confluent-/Apicurio-compatible
// Schema Registry endpoint. When URL is empty, SR features are disabled for that cluster.
type SchemaRegistryConfig struct {
	URL                string `koanf:"url"`
	Username           string `koanf:"username"`
	Password           string `koanf:"password"`
	InsecureSkipVerify bool   `koanf:"insecure_skip_verify"`
}

// AuthConfig is per-cluster authentication. Type "" or "none" disables SASL.
// Valid types: "none", "plain", "scram-sha-256", "scram-sha-512".
type AuthConfig struct {
	Type     string `koanf:"type"`
	Username string `koanf:"username"`
	Password string `koanf:"password"`
}

// TLSConfig enables TLS for the cluster connection. When Enabled is true,
// franz-go uses the system trust store unless InsecureSkipVerify is set.
type TLSConfig struct {
	Enabled            bool `koanf:"enabled"`
	InsecureSkipVerify bool `koanf:"insecure_skip_verify"`
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
		Server: ServerConfig{Addr: ":37421"},
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

	if err := k.Load(env.Provider("KAFKITO_", ".", envKeyTransform), nil); err != nil {
		return Config{}, fmt.Errorf("load env: %w", err)
	}

	var out Config
	if err := k.Unmarshal("", &out); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}

	applyShortcuts(&out)

	if err := out.Validate(); err != nil {
		return Config{}, err
	}
	return out, nil
}

// Validate returns an error for structurally invalid configurations.
// It is intentionally lenient: zero clusters is allowed (kafkito still
// starts, but cluster-scoped endpoints will report unavailable).
func (c Config) Validate() error {
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

// envKeyTransform maps e.g. KAFKITO_SERVER_ADDR -> server.addr.
// Known list-type keys (brokers) split on comma.
func envKeyTransform(key string) string {
	key = strings.ToLower(strings.TrimPrefix(key, "KAFKITO_"))
	return strings.ReplaceAll(key, "_", ".")
}

// applyShortcuts synthesizes a default cluster from KAFKITO_KAFKA_BROKERS
// when no clusters are otherwise configured.
func applyShortcuts(c *Config) {
	if len(c.Clusters) > 0 {
		return
	}
	raw := os.Getenv("KAFKITO_KAFKA_BROKERS")
	if raw == "" {
		return
	}
	brokers := splitCSV(raw)
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
