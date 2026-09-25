// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
)

// Capabilities reports which admin-level operations the configured Kafka
// user on a cluster is allowed to perform. Missing capabilities are NOT
// fatal: kafkito degrades gracefully and marks the corresponding UI parts
// as disabled with the permission it would need.
type Capabilities struct {
	DescribeCluster bool `json:"describe_cluster"`
	ListTopics      bool `json:"list_topics"`
	DescribeConfigs bool `json:"describe_configs"`
	ListGroups      bool `json:"list_groups"`

	// Write capabilities — probed with validate-only / dry-run API calls.
	CreateTopic  bool `json:"create_topic"`
	DeleteTopic  bool `json:"delete_topic"`
	AlterConfigs bool `json:"alter_configs"`

	// Errors carries per-probe error messages so the UI can surface the
	// concrete reason (e.g. "CLUSTER_AUTHORIZATION_FAILED").
	Errors map[string]string `json:"errors,omitempty"`

	ProbedAt time.Time `json:"probed_at"`
}

type capCache struct {
	caps *Capabilities
	at   time.Time
}

const capCacheTTL = 60 * time.Second

var capCaches sync.Map // key: cluster name

// Capabilities returns the capability probe result for the named cluster,
// using a 60-second cache.
func (r *Registry) Capabilities(ctx context.Context, cluster string) (*Capabilities, error) {
	if _, ok := r.clusters[cluster]; !ok {
		return nil, ErrUnknownCluster
	}
	if v, ok := capCaches.Load(cluster); ok {
		c := v.(*capCache)
		if time.Since(c.at) < capCacheTTL {
			return c.caps, nil
		}
	}
	caps, err := r.probeCapabilities(ctx, cluster)
	if err != nil {
		return nil, err
	}
	capCaches.Store(cluster, &capCache{caps: caps, at: time.Now()})
	return caps, nil
}

// RefreshCapabilities invalidates the cache entry for one cluster.
func (r *Registry) RefreshCapabilities(cluster string) {
	capCaches.Delete(cluster)
}

func (r *Registry) probeCapabilities(ctx context.Context, cluster string) (*Capabilities, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	caps := &Capabilities{
		Errors:   map[string]string{},
		ProbedAt: time.Now(),
	}

	// All probes run sequentially with short timeouts. A failing probe
	// only flips its own flag, never aborts the whole thing.
	probe := func(name string, fn func(ctx context.Context) error) bool {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := fn(pctx); err != nil {
			caps.Errors[name] = classifyErr(err)
			return false
		}
		return true
	}

	caps.DescribeCluster = probe("describe_cluster", func(ctx context.Context) error {
		bds, err := adm.ListBrokers(ctx)
		if err != nil {
			return err
		}
		if len(bds) == 0 {
			return errors.New("no brokers returned")
		}
		return nil
	})

	caps.ListTopics = probe("list_topics", func(ctx context.Context) error {
		_, err := adm.ListTopics(ctx)
		return err
	})

	caps.DescribeConfigs = probe("describe_configs", func(ctx context.Context) error {
		// Probe with an empty topic name list -> brokers reject unauthorized
		// callers with TOPIC_AUTHORIZATION_FAILED / CLUSTER_AUTHORIZATION_FAILED.
		// Use a very unlikely topic name so success doesn't depend on
		// a topic existing.
		rcs, err := adm.DescribeTopicConfigs(ctx, "__kafkito_probe__")
		if err != nil {
			return err
		}
		var firstErr error
		for _, rc := range rcs {
			if rc.Err == nil {
				return nil
			}
			if errors.Is(rc.Err, kerr.UnknownTopicOrPartition) {
				// We have DESCRIBE on configs but the topic is absent -> good.
				return nil
			}
			if firstErr == nil {
				firstErr = rc.Err
			}
		}
		return firstErr
	})

	caps.ListGroups = probe("list_groups", func(ctx context.Context) error {
		_, err := adm.ListGroups(ctx)
		return err
	})

	caps.CreateTopic = probe("create_topic", func(ctx context.Context) error {
		rs, err := adm.ValidateCreateTopics(ctx, 1, 1, nil, "__kafkito_probe_create__")
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.Err == nil {
				return nil
			}
			// TOPIC_ALREADY_EXISTS means we're authorized to create; topic just exists.
			if errors.Is(r.Err, kerr.TopicAlreadyExists) {
				return nil
			}
			return r.Err
		}
		return nil
	})

	caps.DeleteTopic = probe("delete_topic", func(ctx context.Context) error {
		// DeleteTopics has no validate-only. Attempting to delete a nonexistent
		// topic returns UNKNOWN_TOPIC_OR_PARTITION when authorized and
		// TOPIC_AUTHORIZATION_FAILED when not.
		rs, err := adm.DeleteTopics(ctx, "__kafkito_probe_delete_never_exists__")
		if err != nil {
			return err
		}
		r, ok := rs["__kafkito_probe_delete_never_exists__"]
		if !ok {
			return nil
		}
		if r.Err == nil || errors.Is(r.Err, kerr.UnknownTopicOrPartition) {
			return nil
		}
		return r.Err
	})

	caps.AlterConfigs = probe("alter_configs", func(ctx context.Context) error {
		rs, err := adm.ValidateAlterTopicConfigs(ctx,
			[]kadm.AlterConfig{{Op: kadm.SetConfig, Name: "retention.ms", Value: strPtr("60000")}},
			"__kafkito_probe_alter__",
		)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			return nil
		}
		r := rs[0]
		if r.Err == nil || errors.Is(r.Err, kerr.UnknownTopicOrPartition) {
			return nil
		}
		return r.Err
	})

	return caps, nil
}

func strPtr(s string) *string { return &s }

// classifyErr returns a compact, UI-friendly label for an error.
func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	if ke := (*kerr.Error)(nil); errors.As(err, &ke) {
		return ke.Message
	}
	return err.Error()
}
