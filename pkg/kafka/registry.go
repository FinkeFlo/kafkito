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
	"net"
	"strings"
	"sync"
	"time"

	"github.com/FinkeFlo/kafkito/pkg/config"
	"github.com/FinkeFlo/kafkito/pkg/masking"
	"github.com/FinkeFlo/kafkito/pkg/netguard"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
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
	IsProd         bool          `json:"is_prod"`
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
	// adhocFPKeyOnce/adhocFPKeyVal hold the process-local secret used to key
	// the ad-hoc cluster fingerprint HMAC (see adhoc.go Fingerprint). Lazily
	// generated on first use so registries that never see a private-cluster
	// request pay no cost.
	adhocFPKeyOnce sync.Once
	adhocFPKeyVal  []byte

	srMu       sync.Mutex
	srDecoders map[string]*SRDecoder

	// cfgCacheMu guards cfgCache.
	cfgCacheMu sync.Mutex
	// cfgCache holds recent DescribeTopicConfigs results keyed by
	// "cluster\x00topic". Permanent errors (e.g. unauthorized) are cached
	// for cfgCacheTTLPermanent; successful reads for cfgCacheTTLSuccess.
	// This avoids a Kafka round-trip on every frontend poll interval.
	cfgCache map[string]topicConfigsCacheEntry

	// metrics is lazily started; nil until StartMetrics is called.
	metrics *metricsCollector
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
		cfgCache:   make(map[string]topicConfigsCacheEntry),
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

// ConfigFor returns the ClusterConfig registered under the given internal
// name (static or ad-hoc/private). Used by the HTTP layer to check
// cluster-level flags (e.g. IsProd) before performing a mutating operation.
func (r *Registry) ConfigFor(name string) (config.ClusterConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, ok := r.clusters[name]
	return cfg, ok
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
//
// For ad-hoc (private) clusters a dial-time SSRF guard is installed via a
// single kgo.Dialer so that broker connections cannot be redirected to the
// cloud metadata endpoint by DNS rebinding (finding #4). When TLS is enabled
// for an ad-hoc cluster the TLS handshake is performed INSIDE that guarded
// dialer (see guardedTLSDialer) rather than via kgo.DialTLSConfig: franz-go
// rejects setting both kgo.Dialer and kgo.DialTLSConfig together. Operator-
// configured clusters keep the default kgo dialer plus kgo.DialTLSConfig — no
// behavior change for them.
func clientOpts(cfg config.ClusterConfig, log *slog.Logger) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID("kafkito"),
		kgo.WithLogger(kgoSlogAdapter{log: log}),
		kgo.MetadataMaxAge(30 * time.Second),
		kgo.RequestTimeoutOverhead(5 * time.Second),
		// Honour a caller-chosen Record.Partition; kgo's default partitioner
		// overwrites it (see explicitOrKeyPartitioner).
		kgo.RecordPartitioner(explicitOrKeyPartitioner()),
	}

	if cfg.TLS.Enabled && cfg.TLS.InsecureSkipVerify {
		log.Warn("TLS verification disabled for cluster (InsecureSkipVerify=true)",
			slog.String("cluster", cfg.Name))
	}

	// Ad-hoc clusters originate from untrusted user-supplied broker addresses,
	// so the dial is guarded against DNS-rebinding SSRF (finding #4, MEDIUM).
	// Operator-configured clusters are intentionally unguarded — they may
	// legitimately point at localhost or internal addresses.
	if IsAdhoc(cfg.Name) {
		// A single dialer covers both the SSRF guard and (when enabled) the
		// TLS handshake. We must NOT also pass kgo.DialTLSConfig here, because
		// franz-go errors out if Dialer and DialTLSConfig are both set.
		opts = append(opts, kgo.Dialer(guardedTLSDialer(cfg.TLS)))
	} else if cfg.TLS.Enabled {
		// Operator clusters: keep the original DialTLSConfig path unchanged.
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

// guardedTLSDialer returns a kgo.Dialer-compatible dial function for ad-hoc
// (private) clusters. It always routes the connection through the SSRF guard
// (netguard.GuardedDialContext), which validates every resolved address and
// dials the validated IP literal to close the resolve->dial TOCTOU window.
//
// When TLS is enabled it performs the TLS handshake itself on top of the
// guarded connection, instead of relying on kgo.DialTLSConfig (which cannot be
// combined with kgo.Dialer in franz-go). Because the guard dials an IP literal,
// the per-dial tls.Config.ServerName is set to the original hostname parsed
// from the dialer's addr argument so certificate verification still works; the
// shared base config is cloned per dial to avoid concurrent mutation.
func guardedTLSDialer(tlsCfg config.TLSConfig) func(ctx context.Context, network, host string) (net.Conn, error) {
	guarded := netguard.GuardedDialContext(&net.Dialer{Timeout: 10 * time.Second})
	if !tlsCfg.Enabled {
		return guarded
	}
	// #nosec G402 -- InsecureSkipVerify is operator/user-controlled and
	// documented for dev/self-signed setups; mirrors the DialTLSConfig path.
	base := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
	}
	return func(ctx context.Context, network, host string) (net.Conn, error) {
		conn, err := guarded(ctx, network, host)
		if err != nil {
			return nil, err
		}
		// Set ServerName to the original hostname (not the validated IP we
		// actually dialed) so cert verification matches the SAN/CN. Fall back
		// to the raw host if it has no port (defensive; kgo always passes one).
		serverName := host
		if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			serverName = h
		}
		cfg := base.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = serverName
		}
		tlsConn := tls.Client(conn, cfg)
		if hsErr := tlsConn.HandshakeContext(ctx); hsErr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("tls handshake to %s: %w", host, hsErr)
		}
		return tlsConn, nil
	}
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
	if snap, ok := r.ClusterMetricsSnapshot(cluster); ok {
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
func (r *Registry) describeCachedTopicConfigs(ctx context.Context, cluster, topic string, adm *kadm.Client) ([]TopicConfigEntry, string) {
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
		r.log.Warn("describe topic configs failed", "cluster", cluster, "topic", topic, "err", err)
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

	return configs, configsErr
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
