// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PrivateClusterMode says who may use private clusters, the clusters a user
// defines in the browser and sends per request (see PrivateClusterSentinel).
type PrivateClusterMode string

// The values of PrivateClustersConfig.Mode.
const (
	// PrivateClustersOn lets every user use private clusters. It is the
	// default.
	PrivateClustersOn PrivateClusterMode = "on"
	// PrivateClustersOff disables private clusters.
	PrivateClustersOff PrivateClusterMode = "off"
	// PrivateClustersRole limits private clusters to RBAC subjects with the
	// permission private_cluster:use. It requires rbac.enabled.
	PrivateClustersRole PrivateClusterMode = "role"
)

// The koanf keys of PrivateClustersConfig.
const (
	privateClustersKey      = "private_clusters"
	privateClustersModeKey  = "private_clusters.mode"
	allowPlainWithoutTLSKey = "private_clusters.allow_plain_without_tls"
)

// PrivateClustersConfig is the private_clusters block. Settings for private
// clusters are fields of this block next to Mode.
type PrivateClustersConfig struct {
	// Mode is on, off or role, case-insensitive; empty means on. Load also
	// accepts a YAML boolean: true is on, false is off.
	Mode PrivateClusterMode `koanf:"mode"`
	// AllowPlainWithoutTLS lets a private cluster use SASL/PLAIN without
	// TLS. By default such a definition is rejected, because PLAIN sends
	// the password in clear text. Configured clusters are not affected.
	AllowPlainWithoutTLS bool `koanf:"allow_plain_without_tls"`
}

// EffectiveMode returns Mode trimmed and lowercased, PrivateClustersOn when
// Mode is empty.
func (p PrivateClustersConfig) EffectiveMode() PrivateClusterMode {
	m := PrivateClusterMode(strings.ToLower(strings.TrimSpace(string(p.Mode))))
	if m == "" {
		return PrivateClustersOn
	}
	return m
}

func (p PrivateClustersConfig) validate(rbacEnabled bool) error {
	switch p.EffectiveMode() {
	case PrivateClustersOn, PrivateClustersOff:
		return nil
	case PrivateClustersRole:
		if !rbacEnabled {
			return errors.New(`private_clusters.mode "role" requires rbac.enabled: true`)
		}
		return nil
	default:
		return fmt.Errorf("private_clusters.mode %q not supported (use on|off|role)", p.Mode)
	}
}

// parseBoolSetting returns the boolean value v of the koanf key key: a YAML
// boolean as is, a string (env var, quoted YAML) parsed by strconv.ParseBool
// after trimming, where an empty string is false. Any other value is an
// error.
func parseBoolSetting(key string, v any) (bool, error) {
	switch v := v.(type) {
	case bool:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return false, fmt.Errorf("%s %q is not a boolean (use true|false)", key, v)
		}
		return b, nil
	default:
		return false, fmt.Errorf("%s %v is not a boolean (use true|false)", key, v)
	}
}

// privateClusterModeOf maps a YAML boolean mode to on (true) or off (false).
func privateClusterModeOf(b bool) PrivateClusterMode {
	if b {
		return PrivateClustersOn
	}
	return PrivateClustersOff
}

// AdhocClusterPrefix starts the internal registry name of every private
// cluster: the kafka package registers a header-provided configuration
// under this prefix plus a fingerprint. It is the only definition of the
// prefix. Configured clusters may not use it (see Validate), and the HTTP
// layer never accepts such a name from a client.
const AdhocClusterPrefix = "__adhoc_"

// IsAdhocClusterName reports whether name is in the internal namespace of
// private clusters.
func IsAdhocClusterName(name string) bool {
	return strings.HasPrefix(name, AdhocClusterPrefix)
}

// PublicClusterName returns the cluster name a client uses for the registry
// name name: PrivateClusterSentinel for a private cluster, name itself
// otherwise. Responses and error messages name clusters with it.
func PublicClusterName(name string) string {
	if IsAdhocClusterName(name) {
		return PrivateClusterSentinel
	}
	return name
}

// clusterLogIDDomain separates the log id hash from any other hash over a
// cluster name.
const clusterLogIDDomain = "kafkito/private-cluster-log-id\x00"

// ClusterLogName returns how logs name the cluster with registry name name.
// A private cluster is "private-" and 12 hex digits of a SHA-256 over its
// registry name: the same on every log line of that cluster while the
// process runs, so the lines correlate, without the registry name itself.
// Any other name is returned unchanged.
func ClusterLogName(name string) string {
	if !IsAdhocClusterName(name) {
		return name
	}
	sum := sha256.Sum256([]byte(clusterLogIDDomain + name))
	return "private-" + hex.EncodeToString(sum[:6])
}
