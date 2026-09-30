// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"strings"
	"time"
)

// ClusterInfo describes a configured cluster and whether it is currently reachable.
type ClusterInfo struct {
	Name           string        `json:"name"`
	Reachable      bool          `json:"reachable"`
	Error          string        `json:"error,omitempty"`
	IsProd         bool          `json:"is_prod"`
	AuthType       string        `json:"auth_type"`
	TLS            bool          `json:"tls"`
	SchemaRegistry bool          `json:"schema_registry"`
	Capabilities   *Capabilities `json:"capabilities,omitempty"`
	// BrokerIssues lists the advertised brokers a Test connection could not
	// reach (see Connections.ProbeBrokers). Only the Test connection
	// endpoint fills it; a non-empty list implies Reachable is false.
	BrokerIssues []BrokerIssue `json:"broker_issues,omitempty"`
	// BrokersSkipped is how many advertised brokers a Test connection did
	// not check because the probe caps how many it dials. Only the Test
	// connection endpoint fills it; on its own it does not make the
	// cluster unreachable.
	BrokersSkipped int `json:"brokers_skipped,omitempty"`
	// Aggregate counts and metrics (filled best-effort from the metrics
	// collector; nil when unknown yet or when the cluster is unreachable).
	Brokers         *int     `json:"brokers,omitempty"`
	Topics          *int     `json:"topics,omitempty"`
	Groups          *int     `json:"groups,omitempty"`
	TotalMessages   *int64   `json:"total_messages,omitempty"`
	TotalLag        *int64   `json:"total_lag,omitempty"`
	TotalRatePerSec *float64 `json:"total_rate_per_sec,omitempty"`
}

// Describe returns ClusterInfo for every configured cluster, each probed
// with the given per-cluster timeout. If probeCaps is true, the capability
// probe is also attached (using the 60s cache).
func (r *Clusters) Describe(ctx context.Context, probeTimeout time.Duration) []ClusterInfo {
	configs := r.ConfigsOrdered()
	out := make([]ClusterInfo, 0, len(configs))
	for _, c := range configs {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := r.Ping(pctx, c.Name)
		cancel()
		authType := strings.ToLower(strings.TrimSpace(c.Auth.Type))
		if authType == "" {
			authType = "none"
		}
		info := ClusterInfo{
			Name:           c.Name,
			Reachable:      err == nil,
			IsProd:         c.IsProd,
			AuthType:       authType,
			TLS:            c.TLS.Enabled,
			SchemaRegistry: strings.TrimSpace(c.SchemaRegistry.URL) != "",
		}
		if err != nil {
			info.Error = err.Error()
		} else {
			cctx, ccancel := context.WithTimeout(ctx, 4*time.Second)
			if caps, err := r.Capabilities(cctx, c.Name); err == nil {
				info.Capabilities = caps
			}
			ccancel()
			r.applyClusterAggregates(&info)
		}
		out = append(out, info)
	}
	return out
}
