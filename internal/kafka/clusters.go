// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/connerr"
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
	// ErrorClass is the class of Error when the cluster could not be
	// reached (see connerr): a configured cluster's ping in Describe, or the
	// seed or broker list of a Test connection. Error is then the class's
	// fixed text.
	ErrorClass connerr.Class `json:"error_class,omitempty"`
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
// with the given per-cluster timeout. A reachable cluster also gets its
// capabilities (using the 60s cache) and the collected aggregates. An
// unreachable one gets the class of the ping error and its fixed text; the
// full error, which can name broker addresses, goes to the log only (see
// notePing).
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
		r.notePing(ctx, c.Name, err)
		if err != nil {
			info.ErrorClass = connerr.Classify(err)
			info.Error = info.ErrorClass.Message()
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

// notePing logs the outcome of a configured cluster's ping in Describe. The
// cluster list is polled by every open UI and /readyz by the orchestrator,
// so a failure is logged when it starts and when its class changes, not on
// every poll, and the recovery once. A ping that failed because the caller
// went away says nothing about the cluster and is ignored.
func (r *Clusters) notePing(ctx context.Context, cluster string, err error) {
	if err != nil && ctx.Err() != nil {
		return
	}
	class := connerr.Classify(err)
	r.pingMu.Lock()
	prev, failing := r.pingFailures[cluster]
	if err == nil {
		delete(r.pingFailures, cluster)
	} else {
		if r.pingFailures == nil {
			r.pingFailures = make(map[string]connerr.Class)
		}
		r.pingFailures[cluster] = class
	}
	r.pingMu.Unlock()

	switch {
	case err != nil && (!failing || prev != class):
		r.log.WarnContext(ctx, "cluster not reachable", "cluster", cluster, "error_class", string(class), "err", err)
	case err == nil && failing:
		r.log.InfoContext(ctx, "cluster reachable again", "cluster", cluster)
	}
}
