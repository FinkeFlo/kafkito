// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package kafka wraps the franz-go client/kadm admin for kafkito.
//
// A Registry owns one *kgo.Client + kadm.Client per configured cluster.
// Clients are created lazily on first use and reused for the process'
// lifetime. Call Close() on shutdown to release all connections.
package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/masking"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ErrUnknownCluster is returned when a lookup targets a non-configured cluster.
var ErrUnknownCluster = errors.New("unknown cluster")

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

// ClusterInfo describes a configured cluster and whether it is currently reachable.
type ClusterInfo struct {
	Name           string        `json:"name"`
	Reachable      bool          `json:"reachable"`
	Error          string        `json:"error,omitempty"`
	AuthType       string        `json:"auth_type"`
	TLS            bool          `json:"tls"`
	SchemaRegistry bool          `json:"schema_registry"`
	Capabilities   *Capabilities `json:"capabilities,omitempty"`
	// Aggregate counts and metrics (filled best-effort from the metrics
	// collector; nil when unknown yet or when the cluster is unreachable).
	Brokers         *int     `json:"brokers,omitempty"`
	Topics          *int     `json:"topics,omitempty"`
	Groups          *int     `json:"groups,omitempty"`
	TotalMessages   *int64   `json:"total_messages,omitempty"`
	TotalLag        *int64   `json:"total_lag,omitempty"`
	TotalRatePerSec *float64 `json:"total_rate_per_sec,omitempty"`
}

// Registry is the kafkito-wide set of Kafka clients, keyed by cluster name.
type Registry struct {
	log      *slog.Logger
	ordered  []config.ClusterConfig
	clusters map[string]config.ClusterConfig
	masking  map[string]*masking.Policy

	mu      sync.Mutex
	clients map[string]*kgo.Client
	// adhocLastUsed tracks last-access time for ad-hoc (private) cluster
	// entries so they can be idle-evicted. Nil for registries without any
	// ad-hoc activity. Protected by r.mu.
	adhocLastUsed map[string]time.Time

	srMu       sync.Mutex
	srDecoders map[string]*SRDecoder

	// metrics is lazily started; nil until StartMetrics is called.
	metrics *metricsCollector
}

// NewRegistry constructs a registry from the configured clusters.
func NewRegistry(cfg []config.ClusterConfig, log *slog.Logger) *Registry {
	m := make(map[string]config.ClusterConfig, len(cfg))
	ordered := make([]config.ClusterConfig, len(cfg))
	copy(ordered, cfg)
	for _, c := range cfg {
		m[c.Name] = c
	}
	if log == nil {
		log = slog.Default()
	}
	policies := make(map[string]*masking.Policy, len(cfg))
	for _, c := range cfg {
		p, err := masking.Compile(c.DataMasking)
		if err != nil {
			log.Warn("data masking compile failed", "cluster", c.Name, "error", err)
			p, _ = masking.Compile(nil)
		}
		policies[c.Name] = p
		if c.SchemaRegistry.URL != "" && c.SchemaRegistry.InsecureSkipVerify {
			log.Warn("Schema Registry TLS verification disabled (InsecureSkipVerify=true)",
				slog.String("cluster", c.Name),
				slog.String("url", c.SchemaRegistry.URL))
		}
	}
	return &Registry{
		log:        log,
		ordered:    ordered,
		clusters:   m,
		masking:    policies,
		clients:    make(map[string]*kgo.Client),
		srDecoders: make(map[string]*SRDecoder),
	}
}

// srDecoderFor returns a cached *SRDecoder for the cluster, or nil when the
// cluster has no Schema Registry configured. Decoders are cached for the
// lifetime of the registry.
func (r *Registry) srDecoderFor(cluster string) *SRDecoder {
	r.srMu.Lock()
	defer r.srMu.Unlock()
	if d, ok := r.srDecoders[cluster]; ok {
		return d
	}
	sr, err := r.SchemaRegistry(cluster)
	if err != nil {
		r.srDecoders[cluster] = nil
		return nil
	}
	d := NewSRDecoder(sr)
	r.srDecoders[cluster] = d
	return d
}

// MaskingPolicy returns the compiled masking policy for the named cluster.
// Returns an empty policy if the cluster is unknown or no rules configured.
func (r *Registry) MaskingPolicy(cluster string) *masking.Policy {
	if p, ok := r.masking[cluster]; ok && p != nil {
		return p
	}
	empty, _ := masking.Compile(nil)
	return empty
}

// Names returns the configured cluster names in config order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.ordered))
	for _, c := range r.ordered {
		out = append(out, c.Name)
	}
	return out
}

// ConfigsOrdered returns cluster configs in the order they were registered.
func (r *Registry) ConfigsOrdered() []config.ClusterConfig {
	out := make([]config.ClusterConfig, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// Client returns (or creates) a kgo.Client for the given cluster name.
func (r *Registry) Client(name string) (*kgo.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.clients[name]; ok {
		return c, nil
	}
	cfg, ok := r.clusters[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, name)
	}

	cl, err := kgo.NewClient(clientOpts(cfg, r.log.With("cluster", name))...)
	if err != nil {
		return nil, fmt.Errorf("kgo.NewClient for %s: %w", name, err)
	}
	r.clients[name] = cl
	return cl, nil
}

// clientOpts builds the kgo option slice for a configured cluster,
// including SASL and TLS when requested.
func clientOpts(cfg config.ClusterConfig, log *slog.Logger) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID("kafkito"),
		kgo.WithLogger(kgoSlogAdapter{log: log}),
		kgo.MetadataMaxAge(30 * time.Second),
		kgo.RequestTimeoutOverhead(5 * time.Second),
	}

	if cfg.TLS.Enabled {
		if cfg.TLS.InsecureSkipVerify {
			log.Warn("TLS verification disabled for cluster (InsecureSkipVerify=true)",
				slog.String("cluster", cfg.Name))
		}
		// #nosec G402 -- InsecureSkipVerify is operator-controlled and
		// documented for dev/self-signed setups.
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		}))
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Auth.Type)) {
	case "", "none":
		// no SASL
	case "plain":
		opts = append(opts, kgo.SASL(plain.Auth{
			User: cfg.Auth.Username,
			Pass: cfg.Auth.Password,
		}.AsMechanism()))
	case "scram-sha-256":
		opts = append(opts, kgo.SASL(scram.Auth{
			User: cfg.Auth.Username,
			Pass: cfg.Auth.Password,
		}.AsSha256Mechanism()))
	case "scram-sha-512":
		opts = append(opts, kgo.SASL(scram.Auth{
			User: cfg.Auth.Username,
			Pass: cfg.Auth.Password,
		}.AsSha512Mechanism()))
	}

	return opts
}

// Admin returns a kadm.Client bound to the named cluster's kgo.Client.
func (r *Registry) Admin(name string) (*kadm.Client, error) {
	cl, err := r.Client(name)
	if err != nil {
		return nil, err
	}
	return kadm.NewClient(cl), nil
}

// Ping probes every broker of the named cluster. Returns the first error.
//
// Note: franz-go's kgo.Client.Ping fans out an ApiVersions request to
// every broker advertised by the cluster's metadata response. On a cold
// kgo client each broker requires its own DNS + TCP + TLS + SASL
// handshake, so callers must budget time × broker_count when probing
// remote SaaS clusters (e.g. Confluent Cloud advertises N brokers via
// `bN-pkc-…` hostnames). The user-facing Test connection handler in
// internal/server/clusters.go uses a 15s budget for that reason.
func (r *Registry) Ping(ctx context.Context, name string) error {
	cl, err := r.Client(name)
	if err != nil {
		return err
	}
	return cl.Ping(ctx)
}

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
	// SizeBytes is the leader-replica byte sum from the metrics collector.
	// Nil when the collector has no snapshot yet for this topic, distinct
	// from "known zero".
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

// ListTopics returns topic summaries for the named cluster.
// Internal topics (starting with "__") are included and flagged.
func (r *Registry) ListTopics(ctx context.Context, name string) ([]TopicInfo, error) {
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
	if mc := r.metricsCollector(); mc != nil {
		mc.ensureFresh(ctx, name, privateClusterMetricsTTL, adm)
	}
	r.applyTopicMetrics(name, out)
	return out, nil
}

// DescribeTopic returns full metadata + configs + offsets for a topic.
func (r *Registry) DescribeTopic(ctx context.Context, cluster, topic string) (*TopicDetail, error) {
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

	configs := []TopicConfigEntry{}
	rcs, err := adm.DescribeTopicConfigs(ctx, topic)
	if err == nil {
		for _, rc := range rcs {
			if rc.Err != nil {
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
		r.log.Warn("describe topic configs failed", "cluster", cluster, "topic", topic, "err", err)
	}

	out := &TopicDetail{
		Name:              topic,
		IsInternal:        t.IsInternal,
		Partitions:        parts,
		ReplicationFactor: rf,
		Messages:          total,
		Configs:           configs,
	}
	if snap, ok := r.ClusterMetricsSnapshot(cluster); ok {
		if m, ok := snap.PerTopic[topic]; ok && m.HaveSize {
			out.SizeBytes = ptrInt64(m.SizeBytes)
		}
	}
	return out, nil
}

// Describe returns ClusterInfo for every configured cluster, each probed
// with the given per-cluster timeout. If probeCaps is true, the capability
// probe is also attached (using the 60s cache).
func (r *Registry) Describe(ctx context.Context, probeTimeout time.Duration) []ClusterInfo {
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

// Close releases all underlying Kafka clients.
func (r *Registry) Close() {
	r.mu.Lock()
	mc := r.metrics
	r.metrics = nil
	r.mu.Unlock()
	if mc != nil {
		mc.stop()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, cl := range r.clients {
		cl.Close()
		delete(r.clients, name)
	}
}

// kgoSlogAdapter bridges kgo's internal logger to slog.
type kgoSlogAdapter struct {
	log *slog.Logger
}

func (a kgoSlogAdapter) Level() kgo.LogLevel { return kgo.LogLevelWarn }

func (a kgoSlogAdapter) Log(level kgo.LogLevel, msg string, keyvals ...any) {
	switch level {
	case kgo.LogLevelError:
		a.log.Error(msg, keyvals...)
	case kgo.LogLevelWarn:
		a.log.Warn(msg, keyvals...)
	case kgo.LogLevelInfo:
		a.log.Info(msg, keyvals...)
	default:
		a.log.Debug(msg, keyvals...)
	}
}
