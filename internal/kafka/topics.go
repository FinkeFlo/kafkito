// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// TopicInfo is a lightweight view of a Kafka topic for list pages. Metric
// fields are filled in best-effort from the metrics collector; a nil pointer
// means "not yet known" (distinct from "zero") so the frontend can render
// a placeholder instead of a misleading 0.
type TopicInfo struct {
	Name              string   `json:"name"`
	Partitions        int      `json:"partitions"`
	ReplicationFactor int      `json:"replication_factor"`
	IsInternal        bool     `json:"is_internal"`
	Messages          *int64   `json:"messages,omitempty"`
	SizeBytes         *int64   `json:"size_bytes,omitempty"`
	RetentionMs       *int64   `json:"retention_ms,omitempty"` // -1 == infinite (retention.ms=-1)
	RatePerSec        *float64 `json:"rate_per_sec,omitempty"`
	Lag               *int64   `json:"lag,omitempty"`
}

// topicConfigsCacheEntry holds one cached DescribeTopicConfigs outcome.
type topicConfigsCacheEntry struct {
	configs    []TopicConfigEntry
	configsErr string
	expiry     time.Time
}

const (
	// cfgCacheTTLPermanent is used for errors that are unlikely to resolve on
	// their own (e.g. missing ACL). Long enough to suppress poll-driven spam
	// without permanently hiding a fix by the cluster admin.
	cfgCacheTTLPermanent = 60 * time.Second
	// cfgCacheTTLSuccess is used for successful reads. Short enough that a
	// config change is reflected quickly.
	cfgCacheTTLSuccess = 10 * time.Second
)

// PartitionInfo describes a single topic partition.
type PartitionInfo struct {
	Partition   int32   `json:"partition"`
	Leader      int32   `json:"leader"`
	Replicas    []int32 `json:"replicas"`
	ISR         []int32 `json:"isr"`
	StartOffset int64   `json:"start_offset"`
	EndOffset   int64   `json:"end_offset"`
	Messages    int64   `json:"messages"`
}

// TopicConfigEntry is a single topic-level config override/default.
type TopicConfigEntry struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	IsDefault bool   `json:"is_default"`
	Source    string `json:"source,omitempty"`
	Sensitive bool   `json:"sensitive"`
}

// TopicDetail is the full metadata view for one topic.
type TopicDetail struct {
	Name              string             `json:"name"`
	IsInternal        bool               `json:"is_internal"`
	Partitions        []PartitionInfo    `json:"partitions"`
	ReplicationFactor int                `json:"replication_factor"`
	Messages          int64              `json:"messages"`
	Configs           []TopicConfigEntry `json:"configs"`
	// ConfigsError signals that DescribeConfigs failed for this topic and
	// callers should treat Configs as incomplete. Empty when configs were
	// read successfully. Known codes: "unauthorized" (missing
	// DescribeConfigs ACL on the topic), "unavailable" (any other broker
	// error). UI surfaces this so users see "permission missing" instead
	// of a silently empty retention / configs view.
	ConfigsError string `json:"configs_error,omitempty"`
	// SizeBytes is the leader-replica byte sum from the metrics collector.
	// Nil when the collector has no snapshot yet for this topic, distinct
	// from "known zero".
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

// ListTopics returns topic summaries for the named cluster.
// Internal topics (starting with "__") are included and flagged.
func (r *Topics) ListTopics(ctx context.Context, name string) ([]TopicInfo, error) {
	adm, err := r.Admin(name)
	if err != nil {
		return nil, err
	}
	md, err := adm.Metadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	out := make([]TopicInfo, 0, len(md.Topics))
	for topicName, t := range md.Topics {
		if t.Err != nil {
			r.log.Warn("topic metadata error", "topic", topicName, "err", t.Err)
			continue
		}
		rf := 0
		for _, p := range t.Partitions {
			if n := len(p.Replicas); n > rf {
				rf = n
			}
		}
		out = append(out, TopicInfo{
			Name:              topicName,
			Partitions:        len(t.Partitions),
			ReplicationFactor: rf,
			IsInternal:        t.IsInternal,
		})
	}
	// For private (browser-stored) clusters the periodic collector has no
	// state entry, so applyTopicMetrics would otherwise no-op. ensureFresh
	// runs an on-demand probe (cached for privateClusterMetricsTTL) and
	// is a fast cache hit for configured clusters.
	if mc := r.stats.metricsCollector(); mc != nil {
		mc.ensureFresh(ctx, name, privateClusterMetricsTTL, adm)
	}
	r.stats.applyTopicMetrics(name, out)
	return out, nil
}

// DescribeTopic returns full metadata + configs + offsets for a topic.
func (r *Topics) DescribeTopic(ctx context.Context, cluster, topic string) (*TopicDetail, error) {
	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}

	md, err := adm.Metadata(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	t, ok := md.Topics[topic]
	if !ok {
		return nil, fmt.Errorf("topic not found: %s", topic)
	}
	if t.Err != nil {
		return nil, fmt.Errorf("topic error: %w", t.Err)
	}

	starts, err := adm.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets: %w", err)
	}
	ends, err := adm.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets: %w", err)
	}

	parts := make([]PartitionInfo, 0, len(t.Partitions))
	rf := 0
	var total int64
	for _, p := range t.Partitions {
		if n := len(p.Replicas); n > rf {
			rf = n
		}
		var startOff, endOff int64
		if so, ok := starts.Lookup(topic, p.Partition); ok {
			startOff = so.Offset
		}
		if eo, ok := ends.Lookup(topic, p.Partition); ok {
			endOff = eo.Offset
		}
		msgs := endOff - startOff
		if msgs < 0 {
			msgs = 0
		}
		total += msgs
		parts = append(parts, PartitionInfo{
			Partition:   p.Partition,
			Leader:      p.Leader,
			Replicas:    append([]int32{}, p.Replicas...),
			ISR:         append([]int32{}, p.ISR...),
			StartOffset: startOff,
			EndOffset:   endOff,
			Messages:    msgs,
		})
	}

	configs, configsErr := r.describeCachedTopicConfigs(ctx, cluster, topic, adm)

	out := &TopicDetail{
		Name:              topic,
		IsInternal:        t.IsInternal,
		Partitions:        parts,
		ReplicationFactor: rf,
		Messages:          total,
		Configs:           configs,
		ConfigsError:      configsErr,
	}
	if snap, ok := r.stats.ClusterMetricsSnapshot(cluster); ok {
		if m, ok := snap.PerTopic[topic]; ok && m.HaveSize {
			out.SizeBytes = ptrInt64(m.SizeBytes)
		}
	}
	return out, nil
}

// classifyConfigsErr maps a DescribeConfigs error to a short, UI-friendly
// code stored in TopicDetail.ConfigsError. Empty means "not classifiable"
// (caller should fall back to a generic code).
func classifyConfigsErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, kerr.TopicAuthorizationFailed) ||
		errors.Is(err, kerr.ClusterAuthorizationFailed) {
		return "unauthorized"
	}
	return "unavailable"
}

// describeCachedTopicConfigs calls DescribeTopicConfigs and caches the result.
// Permanent errors ("unauthorized") are held for cfgCacheTTLPermanent to
// avoid a Kafka round-trip on every frontend poll. Successful reads are cached
// for cfgCacheTTLSuccess so config changes are still reflected quickly.
func (r *Topics) describeCachedTopicConfigs(ctx context.Context, cluster, topic string, adm *kadm.Client) ([]TopicConfigEntry, string) {
	key := cluster + "\x00" + topic
	now := time.Now()

	r.cfgCacheMu.Lock()
	if e, ok := r.cfgCache[key]; ok && now.Before(e.expiry) {
		r.cfgCacheMu.Unlock()
		return e.configs, e.configsErr
	}
	r.cfgCacheMu.Unlock()

	configs := []TopicConfigEntry{}
	var configsErr string

	rcs, err := adm.DescribeTopicConfigs(ctx, topic)
	if err == nil {
		for _, rc := range rcs {
			if rc.Err != nil {
				if code := classifyConfigsErr(rc.Err); code != "" && configsErr == "" {
					configsErr = code
				}
				continue
			}
			for _, c := range rc.Configs {
				val := ""
				if c.Value != nil {
					val = *c.Value
				}
				isDefault := c.Source == kmsg.ConfigSourceDefaultConfig ||
					c.Source == kmsg.ConfigSourceStaticBrokerConfig ||
					c.Source == kmsg.ConfigSourceDynamicDefaultBrokerConfig
				configs = append(configs, TopicConfigEntry{
					Name:      c.Key,
					Value:     val,
					IsDefault: isDefault,
					Source:    c.Source.String(),
					Sensitive: c.Sensitive,
				})
			}
		}
	} else {
		configsErr = classifyConfigsErr(err)
		if configsErr == "" {
			configsErr = "unavailable"
		}
		r.log.Warn("describe topic configs failed", "cluster", config.ClusterLogName(cluster), "topic", topic, "err", err)
	}

	ttl := cfgCacheTTLSuccess
	if configsErr == "unauthorized" {
		ttl = cfgCacheTTLPermanent
	}

	r.cfgCacheMu.Lock()
	r.cfgCache[key] = topicConfigsCacheEntry{
		configs:    configs,
		configsErr: configsErr,
		expiry:     now.Add(ttl),
	}
	r.cfgCacheMu.Unlock()

	// The idle janitor may have evicted the ad-hoc cluster while its configs
	// were read. Its cleanup then ran before the entry above was stored, so
	// drop the entry here.
	if config.IsAdhocClusterName(cluster) && !r.registered(cluster) {
		r.cfgCacheMu.Lock()
		delete(r.cfgCache, key)
		r.cfgCacheMu.Unlock()
	}

	return configs, configsErr
}

// dropTopicConfigs removes the cached topic configs of the named clusters.
// It is an evict hook (see Connections.evictHooks).
func (r *Topics) dropTopicConfigs(clusters []string) {
	r.cfgCacheMu.Lock()
	defer r.cfgCacheMu.Unlock()
	for key := range r.cfgCache {
		cluster, _, _ := strings.Cut(key, "\x00")
		if slices.Contains(clusters, cluster) {
			delete(r.cfgCache, key)
		}
	}
}
