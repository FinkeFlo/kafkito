// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package kafka wraps the franz-go client/kadm admin for kafkito.
//
// Connections owns one *kgo.Client + kadm.Client per configured cluster.
// Clients are created lazily on first use and reused for the process'
// lifetime; those of ad-hoc (private) clusters are closed once idle. Registry
// bundles Connections with the domain operations; call Registry.Close() on
// shutdown to release all connections.
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

	"github.com/FinkeFlo/kafkito/internal/config"
	"github.com/FinkeFlo/kafkito/internal/masking"
	"github.com/FinkeFlo/kafkito/internal/netguard"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ErrUnknownCluster is returned when a lookup targets a non-configured cluster.
var ErrUnknownCluster = errors.New("unknown cluster")

// ProducerBatchMaxBytes is kafkito's client-side cap on a single produced
// record batch, overriding franz-go's 1,000,012-byte default (Kafka's stock
// max.message.bytes). Replaying a recovered full value (see Registry.Produce)
// can legitimately exceed the stock default for topics whose brokers allow
// larger messages. Exported so internal/server can quote the exact limit in
// the 413 it returns when a produce exceeds it.
const ProducerBatchMaxBytes = 10 << 20 // 10 MiB

// Connections is the kafkito-wide set of cluster configs and Kafka clients,
// keyed by cluster name. It also owns what every domain operation needs per
// cluster: ad-hoc (private) cluster registration, the Schema Registry
// decoder and the masking policy.
//
// Locking: mu guards clusters, masking, clients, adhocLastUsed,
// adhocDigests and caps, which UseAdhoc and evictIdleAdhoc modify at
// runtime; srMu guards srDecoders; janitorMu guards janitor and
// janitorClosed. ordered, now, adhocSweepEvery, evictHooks and adhocDial
// are set before the first ad-hoc registration and only read afterwards.
// The lock order is janitorMu -> mu -> srMu: the janitor holds janitorMu
// for a whole run, so no code may take janitorMu while holding another
// lock, and code holding srMu must never take mu or call a method that
// does.
type Connections struct {
	log      *slog.Logger
	ordered  []config.ClusterConfig
	clusters map[string]config.ClusterConfig
	masking  map[string]*masking.Policy

	mu      sync.Mutex
	clients map[string]*kgo.Client
	// adhocLastUsed tracks last-access time for ad-hoc (private) cluster
	// entries so they can be idle-evicted. Nil for registries without any
	// ad-hoc activity.
	adhocLastUsed map[string]time.Time
	// adhocDigests holds the full fingerprint digest (see adhocDigest) of
	// every ad-hoc entry; UseAdhoc reuses an entry only if it matches.
	adhocDigests map[string][]byte
	// caps caches capability probe results per cluster name for
	// capCacheTTL (see Clusters.Capabilities).
	caps map[string]capCache
	// adhocFPKeyOnce/adhocFPKeyVal hold the process-local secret used to key
	// the ad-hoc cluster fingerprint HMAC (see adhocDigest). Lazily
	// generated on first use so registries that never see a private-cluster
	// request pay no cost.
	adhocFPKeyOnce sync.Once
	adhocFPKeyVal  []byte

	srMu       sync.Mutex
	srDecoders map[string]*SRDecoder

	// now is the clock of the ad-hoc last-use times and of idle eviction;
	// adhocSweepEvery is the janitor's interval. Tests replace them.
	now             func() time.Time
	adhocSweepEvery time.Duration
	// evictHooks drop what the services keep per cluster (collected
	// metrics, cached topic configs) for the ad-hoc clusters evictIdleAdhoc
	// removed.
	evictHooks []func(clusters []string)

	// janitor runs evictIdleAdhoc while ad-hoc entries exist; nil while
	// disarmed. janitorClosed is set by Registry.Close and keeps it off.
	janitorMu     sync.Mutex
	janitor       *time.Timer
	janitorClosed bool

	// adhocDial, when non-nil, replaces netguard.GuardedDialContext as the
	// guarded dial of ad-hoc clusters (TLS still wraps it). Never set it
	// outside tests: a func that is not a netguard guarded dial disables
	// the SSRF guard for every private cluster. Tests set it,
	// before the first client is built, to a netguard.GuardedDialWith whose
	// resolver and dial redirect a test host to a local fake broker. It is a
	// field rather than a package variable so parallel tests stay isolated.
	adhocDial dialFunc
}

// dialFunc is the shape of kgo.Dialer and of netguard's guarded dialers.
type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// Topics covers topic metadata, topic configs, records and the consumers
// of a topic.
type Topics struct {
	*Connections
	// stats supplies the collected metrics that enrich topic views.
	stats *Clusters

	// cfgCacheMu guards cfgCache.
	cfgCacheMu sync.Mutex
	// cfgCache holds recent DescribeTopicConfigs results keyed by
	// "cluster\x00topic". Permanent errors (e.g. unauthorized) are cached
	// for cfgCacheTTLPermanent; successful reads for cfgCacheTTLSuccess.
	// This avoids a Kafka round-trip on every frontend poll interval.
	cfgCache map[string]topicConfigsCacheEntry
}

// Groups covers consumer groups and their committed offsets.
type Groups struct {
	*Connections
}

// Messages covers reading (consume, search, count, timeline, raw values)
// and producing records.
type Messages struct {
	*Connections
}

// Security covers ACLs and SCRAM credentials.
type Security struct {
	*Connections
}

// Clusters covers the cluster overview, brokers, capabilities and the
// background metrics collector.
type Clusters struct {
	*Connections

	// metrics is lazily started; nil until StartMetrics is called.
	// Protected by Connections.mu.
	metrics *metricsCollector
}

// Registry bundles the cluster connections with the per-resource services.
// The services are embedded, so their methods are also available on the
// Registry itself.
type Registry struct {
	*Connections
	*Topics
	*Groups
	*Messages
	*Security
	*Clusters
}

// NewRegistry constructs a registry from the configured clusters.
func NewRegistry(cfg []config.ClusterConfig, log *slog.Logger) *Registry {
	conns := newConnections(cfg, log)
	clusters := &Clusters{Connections: conns}
	topics := &Topics{
		Connections: conns,
		stats:       clusters,
		cfgCache:    make(map[string]topicConfigsCacheEntry),
	}
	conns.evictHooks = []func([]string){topics.dropTopicConfigs, clusters.dropMetrics}
	return &Registry{
		Connections: conns,
		Topics:      topics,
		Groups:      &Groups{Connections: conns},
		Messages:    &Messages{Connections: conns},
		Security:    &Security{Connections: conns},
		Clusters:    clusters,
	}
}

// newConnections builds the connection set from the configured clusters.
func newConnections(cfg []config.ClusterConfig, log *slog.Logger) *Connections {
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
	return &Connections{
		log:             log,
		ordered:         ordered,
		clusters:        m,
		masking:         policies,
		clients:         make(map[string]*kgo.Client),
		caps:            make(map[string]capCache),
		srDecoders:      make(map[string]*SRDecoder),
		now:             time.Now,
		adhocSweepEvery: adhocSweepInterval,
	}
}

// srDecoderFor returns a cached *SRDecoder for the cluster, or nil when the
// cluster has no Schema Registry configured. Decoders are cached for the
// lifetime of the cluster entry.
//
// The decoder is built without holding srMu, because SchemaRegistry takes mu
// and the lock order is mu -> srMu. If another goroutine cached a decoder in
// the meantime, that one wins.
func (r *Connections) srDecoderFor(cluster string) *SRDecoder {
	r.srMu.Lock()
	d, ok := r.srDecoders[cluster]
	r.srMu.Unlock()
	if ok {
		return d
	}

	if sr, err := r.SchemaRegistry(cluster); err == nil {
		d = NewSRDecoder(sr)
	}

	r.srMu.Lock()
	if cached, ok := r.srDecoders[cluster]; ok {
		d = cached
	} else {
		r.srDecoders[cluster] = d
	}
	r.srMu.Unlock()

	// The idle janitor may have evicted the cluster while the decoder was
	// built. Its cleanup then ran before the entry above was stored, so drop
	// the entry here instead of leaving it behind for a cluster that is gone.
	if _, ok := r.ConfigFor(cluster); !ok {
		r.srMu.Lock()
		if r.srDecoders[cluster] == d {
			delete(r.srDecoders, cluster)
		}
		r.srMu.Unlock()
	}
	return d
}

// MaskingPolicy returns the compiled masking policy for the named cluster.
// Returns an empty policy if the cluster is unknown or no rules configured.
func (r *Connections) MaskingPolicy(cluster string) *masking.Policy {
	r.mu.Lock()
	r.touchAdhocLocked(cluster)
	p, ok := r.masking[cluster]
	r.mu.Unlock()
	if ok && p != nil {
		return p
	}
	empty, _ := masking.Compile(nil)
	return empty
}

// Names returns the configured cluster names in config order.
func (r *Connections) Names() []string {
	out := make([]string, 0, len(r.ordered))
	for _, c := range r.ordered {
		out = append(out, c.Name)
	}
	return out
}

// ConfigsOrdered returns cluster configs in the order they were registered.
func (r *Connections) ConfigsOrdered() []config.ClusterConfig {
	out := make([]config.ClusterConfig, len(r.ordered))
	copy(out, r.ordered)
	return out
}

// ConfigFor returns the ClusterConfig registered under the given internal
// name (static or ad-hoc/private). Used by the HTTP layer to check
// cluster-level flags (e.g. IsProd) before performing a mutating operation.
// Like Client and MaskingPolicy, it counts as a use of an ad-hoc cluster.
func (r *Connections) ConfigFor(name string) (config.ClusterConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchAdhocLocked(name)
	cfg, ok := r.clusters[name]
	return cfg, ok
}

// Client returns (or creates) a kgo.Client for the given cluster name. It
// counts as a use of an ad-hoc cluster and so postpones its idle eviction.
func (r *Connections) Client(name string) (*kgo.Client, error) {
	return r.client(name, true)
}

// client is Client; use false keeps the call from counting as a use, for
// background work that must not keep an idle ad-hoc cluster alive.
func (r *Connections) client(name string, use bool) (*kgo.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if use {
		r.touchAdhocLocked(name)
	}
	if c, ok := r.clients[name]; ok {
		return c, nil
	}
	cfg, ok := r.clusters[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, config.PublicClusterName(name))
	}

	cl, err := kgo.NewClient(clientOptsDial(cfg, r.log.With("cluster", config.ClusterLogName(name)), r.adhocDial)...)
	if err != nil {
		return nil, fmt.Errorf("kgo.NewClient for %s: %w", config.PublicClusterName(name), err)
	}
	r.clients[name] = cl
	return cl, nil
}

// clientOpts builds the kgo option slice for a configured cluster,
// including SASL and TLS when requested.
//
// For ad-hoc (private) clusters a dial-time SSRF guard is installed via a
// single kgo.Dialer so that broker connections cannot be redirected to the
// cloud metadata endpoint by DNS rebinding. When TLS is enabled
// for an ad-hoc cluster the TLS handshake is performed INSIDE that guarded
// dialer (see guardedTLSDialer) rather than via kgo.DialTLSConfig: franz-go
// rejects setting both kgo.Dialer and kgo.DialTLSConfig together. Operator-
// configured clusters keep the default kgo dialer plus kgo.DialTLSConfig — no
// behavior change for them.
func clientOpts(cfg config.ClusterConfig, log *slog.Logger) []kgo.Opt {
	return clientOptsDial(cfg, log, nil)
}

// clientOptsDial is clientOpts with the guarded dial of ad-hoc clusters
// replaced by adhocDial when it is non-nil (see Connections.adhocDial).
func clientOptsDial(cfg config.ClusterConfig, log *slog.Logger, adhocDial dialFunc) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID("kafkito"),
		kgo.WithLogger(kgoSlogAdapter{log: log}),
		kgo.MetadataMaxAge(30 * time.Second),
		kgo.RequestTimeoutOverhead(5 * time.Second),
		// Honour a caller-chosen Record.Partition; kgo's default partitioner
		// overwrites it (see explicitOrKeyPartitioner).
		kgo.RecordPartitioner(explicitOrKeyPartitioner()),
		// franz-go defaults to a 1,000,012-byte batch cap (Kafka's stock
		// max.message.bytes). Replaying a recovered full value (see
		// registry.Produce) can legitimately exceed that for topics whose
		// brokers allow larger messages, so raise kafkito's client-side cap
		// to 10 MiB. This is independent of the actual broker's
		// max.message.bytes; producing above the destination broker's real
		// limit still fails, just with a broker-reported error instead of
		// this client short-circuiting first.
		kgo.ProducerBatchMaxBytes(10 << 20),
	}

	if cfg.TLS.Enabled && cfg.TLS.InsecureSkipVerify {
		log.Warn("TLS verification disabled for cluster (InsecureSkipVerify=true)",
			slog.String("cluster", config.ClusterLogName(cfg.Name)))
	}

	// Ad-hoc clusters originate from untrusted user-supplied broker addresses,
	// so the dial is guarded against DNS-rebinding SSRF.
	// Operator-configured clusters are intentionally unguarded — they may
	// legitimately point at localhost or internal addresses.
	if config.IsAdhocClusterName(cfg.Name) {
		// A single dialer covers both the SSRF guard and (when enabled) the
		// TLS handshake. We must NOT also pass kgo.DialTLSConfig here, because
		// franz-go errors out if Dialer and DialTLSConfig are both set.
		opts = append(opts, kgo.Dialer(guardedTLSDialer(cfg.TLS, adhocDial)))
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
// guarded replaces netguard.GuardedDialContext when non-nil (tests only).
func guardedTLSDialer(tlsCfg config.TLSConfig, guarded dialFunc) dialFunc {
	if guarded == nil {
		guarded = netguard.GuardedDialContext(&net.Dialer{Timeout: 10 * time.Second})
	}
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
func (r *Connections) Admin(name string) (*kadm.Client, error) {
	cl, err := r.Client(name)
	if err != nil {
		return nil, err
	}
	return kadm.NewClient(cl), nil
}

// Ping checks that the named cluster answers at all. It returns nil as soon
// as ONE broker answers a metadata request: franz-go's kgo.Client.Ping tries
// the already discovered brokers, then the seeds, one after another, and
// stops at the first success (kgo client.go, Client.Ping). It therefore
// says nothing about the other advertised brokers; ProbeBrokers checks
// those. Returns the last error when no broker answers.
func (r *Connections) Ping(ctx context.Context, name string) error {
	cl, err := r.Client(name)
	if err != nil {
		return err
	}
	return cl.Ping(ctx)
}

// Close stops the idle janitor and the metrics collector and releases all
// underlying Kafka clients.
func (r *Registry) Close() {
	r.stopJanitor()
	r.stopMetrics()
	r.closeClients()
}

// closeClients releases all underlying Kafka clients.
func (r *Connections) closeClients() {
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
