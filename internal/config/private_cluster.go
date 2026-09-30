// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package config

import "strings"

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
