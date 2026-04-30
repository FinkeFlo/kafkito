// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package config

import (
	"log/slog"
)

// LogValue implements slog.LogValuer so that AuthConfig is always logged
// with its password field masked, no matter which call site emits it.
func (a AuthConfig) LogValue() slog.Value {
	password := ""
	if a.Password != "" {
		password = "***"
	}
	return slog.GroupValue(
		slog.String("type", a.Type),
		slog.String("username", a.Username),
		slog.String("password", password),
	)
}

// LogValue implements slog.LogValuer so that SchemaRegistryConfig is always
// logged with its password field masked.
func (s SchemaRegistryConfig) LogValue() slog.Value {
	password := ""
	if s.Password != "" {
		password = "***"
	}
	return slog.GroupValue(
		slog.String("url", s.URL),
		slog.String("username", s.Username),
		slog.String("password", password),
		slog.Bool("insecure_skip_verify", s.InsecureSkipVerify),
	)
}

// LogValue implements slog.LogValuer so that ClusterConfig never leaks
// embedded credentials when dumped via slog.Any.
func (c ClusterConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", c.Name),
		slog.Any("brokers", c.Brokers),
		slog.Any("auth", c.Auth),
		slog.Bool("tls_enabled", c.TLS.Enabled),
		slog.Bool("tls_insecure", c.TLS.InsecureSkipVerify),
		slog.Any("schema_registry", c.SchemaRegistry),
		slog.Int("masking_rules", len(c.DataMasking)),
	)
}

// LogValue implements slog.LogValuer for the root Config. Emits a summary that
// is safe to include in startup or diagnostic logs.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("server_addr", c.Server.Addr),
		slog.Int("clusters", len(c.Clusters)),
		slog.Bool("rbac_enabled", c.RBAC.Enabled),
		slog.String("rbac_default_role", c.RBAC.DefaultRole),
		slog.Int("rbac_roles", len(c.RBAC.Roles)),
		slog.Int("rbac_subjects", len(c.RBAC.Subjects)),
	)
}
