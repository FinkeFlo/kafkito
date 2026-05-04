// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package kafka

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// DefaultMetricsInterval is how often the collector refreshes a cluster's
// metrics when no interval is configured. 15s balances freshness (a user
// reloading the page gets a near-current value) against Kafka admin load.
const DefaultMetricsInterval = 15 * time.Second

// privateClusterMetricsTTL is how long an on-demand probe result for a
// private (browser-stored) cluster stays warm before a subsequent
// ListTopics call triggers a re-probe. Short enough to feel fresh, long
// enough that page navigation doesn't pay the full probe cost on every
// click.
const privateClusterMetricsTTL = 30 * time.Second

// TopicMetrics is the derived per-topic metric bundle maintained by the
// metrics collector. All fields are best-effort; callers should treat them
// as "last known good" snapshots, not real-time values.
type TopicMetrics struct {
	Messages    int64   // sum(endOffset - startOffset) across partitions
	SizeBytes   int64   // sum of segment sizes on the leader replicas, across brokers
	RetentionMs int64   // retention.ms; -1 means "infinite" (Kafka convention)
	RatePerSec  float64 // messages/sec derived from last two end-offset samples
	Lag         int64   // sum over all consumer groups of their committed-vs-high-watermark gap

	HaveSize      bool
	HaveRetention bool
	HaveRate      bool
	HaveLag       bool
}

// ClusterMetrics is the aggregated snapshot exposed to callers.
type ClusterMetrics struct {
	Brokers       int
	Topics        int
	Groups        int
	TotalMessages int64
	TotalSize     int64
	TotalLag      int64
	TotalRate     float64

	HaveRate  bool
	HaveLag   bool
	HaveSize  bool
	UpdatedAt time.Time

	PerTopic map[string]TopicMetrics
}

// adminProber is the narrow slice of *kadm.Client that the metrics probe
// pipeline depends on. Narrowing the dependency from the full kadm.Client
// surface to only the seven methods probe() actually issues documents
// the contract at the network boundary; *kadm.Client satisfies it
// implicitly so production wiring is unchanged.
type adminProber interface {
	Metadata(ctx context.Context, topics ...string) (kadm.Metadata, error)
	ListStartOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	DescribeAllLogDirs(ctx context.Context, s kadm.TopicsSet) (kadm.DescribedAllLogDirs, error)
	DescribeTopicConfigs(ctx context.Context, topics ...string) (kadm.ResourceConfigs, error)
	ListGroups(ctx context.Context, filterStates ...string) (kadm.ListedGroups, error)
	FetchManyOffsets(ctx context.Context, groups ...string) kadm.FetchOffsetsResponses
}

// ---- collector internals ---------------------------------------------

type topicSample struct {
	endOffsetSum int64
	at           time.Time
}

type clusterState struct {
	mu       sync.RWMutex
	prev     map[string]topicSample // last end-offset sum per topic
	snapshot ClusterMetrics
	hasSnap  bool

	// probeMu serialises in-flight probes for a single cluster so N
	// concurrent ListTopics calls share one probe + N cache hits, not N
	// probes. It is held only while ensureFresh runs the probe pipeline.
	probeMu sync.Mutex
}

type metricsCollector struct {
	log      *slog.Logger
	reg      *Registry
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}

	statesMu sync.RWMutex
	states   map[string]*clusterState
	running  atomic.Bool
}

// StartMetrics launches the periodic metrics collector. It is safe to call
// more than once (subsequent calls are no-ops). The collector refreshes all
// configured clusters every `interval`; pass 0 for DefaultMetricsInterval.
//
// The provided context governs collector lifetime: cancelling it (or
// calling registry.Close) stops the background goroutine and waits for an
// in-flight refresh to finish.
func (r *Registry) StartMetrics(parent context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultMetricsInterval
	}
	r.mu.Lock()
	if r.metrics != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	mc := &metricsCollector{
		log:      r.log.With("component", "metrics"),
		reg:      r,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		states:   make(map[string]*clusterState, len(r.ordered)),
	}
	for _, c := range r.ordered {
		mc.states[c.Name] = &clusterState{prev: make(map[string]topicSample)}
	}
	r.metrics = mc
	r.mu.Unlock()
	mc.running.Store(true)
	go mc.run()
}

func (mc *metricsCollector) stop() {
	if !mc.running.Swap(false) {
		return
	}
	mc.cancel()
	<-mc.done
}

func (mc *metricsCollector) run() {
	defer close(mc.done)
	// Kick off an initial refresh immediately so the UI isn't blank for an
	// entire interval after boot.
	mc.refreshAll()
	t := time.NewTicker(mc.interval)
	defer t.Stop()
	for {
		select {
		case <-mc.ctx.Done():
			return
		case <-t.C:
			mc.refreshAll()
		}
	}
}

func (mc *metricsCollector) refreshAll() {
	mc.statesMu.RLock()
	names := make([]string, 0, len(mc.states))
	for name := range mc.states {
		names = append(names, name)
	}
	mc.statesMu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			mc.refreshOne(n)
		}(name)
	}
	wg.Wait()
}

// refreshOne computes a fresh snapshot for one cluster and stores it on the
// collector state. Individual sub-probes failing don't abort the whole
// refresh — we record what we can and mark the rest as "not known".
func (mc *metricsCollector) refreshOne(cluster string) {
	mc.statesMu.RLock()
	state, ok := mc.states[cluster]
	mc.statesMu.RUnlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(mc.ctx, 12*time.Second)
	defer cancel()

	adm, err := mc.reg.Admin(cluster)
	if err != nil {
		// Cluster unreachable / unknown → clear freshness but keep the
		// last-known snapshot so the UI doesn't flicker on a transient
		// hiccup.
		mc.log.Debug("metrics: admin unavailable", "cluster", cluster, "err", err)
		return
	}

	snap, ok := mc.probe(ctx, adm, state)
	if !ok {
		return
	}
	state.mu.Lock()
	state.snapshot = snap
	state.hasSnap = true
	state.mu.Unlock()
}

// probe runs the full metrics probe pipeline against `adm`, using `state`
// for prior end-offset samples (rate computation) and updating it with
// fresh ones for next time. Returns the fresh snapshot, or (zero, false)
// if a foundational probe (Metadata) failed and there's nothing useful to
// store. Sub-probe errors are logged at Debug and leave the corresponding
// fields unset.
//
// The caller is responsible for storing the returned snapshot on the
// state under state.mu.Lock(). probe never holds state.mu across network
// calls — only the helpers prevPerTopic / writePrev briefly do.
func (mc *metricsCollector) probe(
	ctx context.Context,
	adm adminProber,
	state *clusterState,
) (ClusterMetrics, bool) {
	// Metadata is the cheap, authoritative source for broker + topic lists.
	md, err := adm.Metadata(ctx)
	if err != nil {
		mc.log.Debug("metrics: metadata failed", "err", err)
		return ClusterMetrics{}, false
	}

	perTopic := make(map[string]TopicMetrics, len(md.Topics))
	topicNames := make([]string, 0, len(md.Topics))
	var totalPartitions int
	for name, t := range md.Topics {
		if t.Err != nil {
			continue
		}
		perTopic[name] = TopicMetrics{}
		topicNames = append(topicNames, name)
		totalPartitions += len(t.Partitions)
	}

	snap := ClusterMetrics{
		Brokers:   len(md.Brokers),
		Topics:    len(topicNames),
		UpdatedAt: time.Now(),
	}

	// Run the independent probes in parallel.
	var (
		starts, ends kadm.ListedOffsets
		logDirs      kadm.DescribedAllLogDirs
		configs      kadm.ResourceConfigs
		groups       kadm.ListedGroups
		offsets      kadm.FetchOffsetsResponses

		startsErr, endsErr, logDirsErr, configsErr, groupsErr, offsetsErr error
	)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		starts, startsErr = adm.ListStartOffsets(ctx, topicNames...)
	}()
	go func() {
		defer wg.Done()
		ends, endsErr = adm.ListEndOffsets(ctx, topicNames...)
	}()
	go func() {
		defer wg.Done()
		logDirs, logDirsErr = adm.DescribeAllLogDirs(ctx, nil)
	}()
	wg.Wait()

	// Configs + groups depend on nothing but also need fresh end offsets
	// for lag, so run them after the first wave.
	wg.Add(2)
	go func() {
		defer wg.Done()
		if len(topicNames) > 0 {
			configs, configsErr = adm.DescribeTopicConfigs(ctx, topicNames...)
		}
	}()
	go func() {
		defer wg.Done()
		groups, groupsErr = adm.ListGroups(ctx)
	}()
	wg.Wait()

	// Fetch committed offsets for every group in one multi-request.
	if groupsErr == nil && len(groups) > 0 {
		offsets = adm.FetchManyOffsets(ctx, groups.Groups()...)
	} else if groupsErr != nil {
		offsetsErr = groupsErr
	}

	// ---- messages + rate (from end - start offsets) -----------------
	if startsErr == nil && endsErr == nil {
		prev := state.prevPerTopic()
		now := snap.UpdatedAt
		for _, name := range topicNames {
			var startSum, endSum int64
			if ends != nil {
				for _, off := range ends[name] {
					if off.Err == nil && off.Offset >= 0 {
						endSum += off.Offset
					}
				}
			}
			if starts != nil {
				for _, off := range starts[name] {
					if off.Err == nil && off.Offset >= 0 {
						startSum += off.Offset
					}
				}
			}
			msgs := endSum - startSum
			if msgs < 0 {
				msgs = 0
			}
			m := perTopic[name]
			m.Messages = msgs
			if p, ok := prev[name]; ok {
				dt := now.Sub(p.at).Seconds()
				if dt > 0 {
					delta := endSum - p.endOffsetSum
					if delta < 0 {
						delta = 0 // retention / topic re-creation; treat as 0
					}
					m.RatePerSec = float64(delta) / dt
					m.HaveRate = true
				}
			}
			perTopic[name] = m
		}
		// Record new baseline for next interval.
		state.writePrev(topicNames, ends, now)
	}

	// ---- size ------------------------------------------------------
	if logDirsErr == nil && logDirs != nil {
		sizeByTopic := make(map[string]int64, len(topicNames))
		for _, dirs := range logDirs {
			for _, dir := range dirs {
				if dir.Err != nil {
					continue
				}
				for topic, parts := range dir.Topics {
					var s int64
					for _, p := range parts {
						if p.Size > 0 {
							s += p.Size
						}
					}
					sizeByTopic[topic] += s
				}
			}
		}
		for name := range perTopic {
			if s, ok := sizeByTopic[name]; ok {
				m := perTopic[name]
				m.SizeBytes = s
				m.HaveSize = true
				perTopic[name] = m
				snap.TotalSize += s
			}
		}
		snap.HaveSize = true
	}

	// ---- retention -------------------------------------------------
	if configsErr == nil {
		for _, rc := range configs {
			if rc.Err != nil {
				continue
			}
			for _, c := range rc.Configs {
				if c.Key != "retention.ms" || c.Value == nil {
					continue
				}
				ms, err := strconv.ParseInt(*c.Value, 10, 64)
				if err != nil {
					continue
				}
				m := perTopic[rc.Name]
				m.RetentionMs = ms
				m.HaveRetention = true
				perTopic[rc.Name] = m
			}
		}
	}

	// ---- lag -------------------------------------------------------
	snap.Groups = len(groups)
	if offsetsErr == nil && endsErr == nil && ends != nil {
		lagByTopic := make(map[string]int64, len(perTopic))
		var totalLag int64
		for _, resp := range offsets {
			if resp.Err != nil {
				continue
			}
			for topic, parts := range resp.Fetched {
				for partition, off := range parts {
					if off.Err != nil {
						continue
					}
					end, ok := ends.Lookup(topic, partition)
					if !ok || end.Err != nil || end.Offset < 0 {
						continue
					}
					diff := end.Offset - off.At
					if diff < 0 {
						continue
					}
					lagByTopic[topic] += diff
					totalLag += diff
				}
			}
		}
		for name, l := range lagByTopic {
			m, ok := perTopic[name]
			if !ok {
				continue
			}
			m.Lag = l
			m.HaveLag = true
			perTopic[name] = m
		}
		// Topics with no group committing still need HaveLag=true so the
		// UI renders "0" instead of "—".
		for name, m := range perTopic {
			if !m.HaveLag {
				m.HaveLag = true
				perTopic[name] = m
			}
		}
		snap.TotalLag = totalLag
		snap.HaveLag = true
	}

	// ---- totals ----------------------------------------------------
	var totalRate float64
	haveAnyRate := false
	for _, m := range perTopic {
		snap.TotalMessages += m.Messages
		if m.HaveRate {
			totalRate += m.RatePerSec
			haveAnyRate = true
		}
	}
	if haveAnyRate {
		snap.TotalRate = totalRate
		snap.HaveRate = true
	}

	snap.PerTopic = perTopic
	return snap, true
}

// ensureFresh guarantees a recent snapshot exists for `cluster`,
// running an on-demand probe via `adm` if one is missing or older than
// `ttl`. Used by ListTopics so private (browser-stored) clusters get the
// same metric fields as configured ones, without spinning up a periodic
// collector for them. Concurrent callers for the same cluster share one
// probe via probeMu.
//
// `adm` is supplied by the caller because for private clusters the admin
// client is built from per-request headers; the registry already caches
// it, so passing it through avoids a duplicate resolve.
func (mc *metricsCollector) ensureFresh(
	ctx context.Context,
	cluster string,
	ttl time.Duration,
	adm adminProber,
) {
	mc.statesMu.RLock()
	state, ok := mc.states[cluster]
	mc.statesMu.RUnlock()
	if !ok {
		mc.statesMu.Lock()
		state, ok = mc.states[cluster] // re-check under write lock
		if !ok {
			state = &clusterState{prev: make(map[string]topicSample)}
			mc.states[cluster] = state
		}
		mc.statesMu.Unlock()
	}

	state.probeMu.Lock()
	defer state.probeMu.Unlock()

	state.mu.RLock()
	fresh := state.hasSnap && time.Since(state.snapshot.UpdatedAt) < ttl
	state.mu.RUnlock()
	if fresh {
		return
	}

	snap, ok := mc.probe(ctx, adm, state)
	if !ok {
		return
	}
	state.mu.Lock()
	state.snapshot = snap
	state.hasSnap = true
	state.mu.Unlock()
}

func (s *clusterState) prevPerTopic() map[string]topicSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]topicSample, len(s.prev))
	for k, v := range s.prev {
		out[k] = v
	}
	return out
}

func (s *clusterState) writePrev(topics []string, ends kadm.ListedOffsets, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Drop entries for topics that vanished so the map doesn't grow
	// unbounded across topic deletes.
	next := make(map[string]topicSample, len(topics))
	for _, name := range topics {
		var sum int64
		if ends != nil {
			for _, off := range ends[name] {
				if off.Err == nil && off.Offset >= 0 {
					sum += off.Offset
				}
			}
		}
		next[name] = topicSample{endOffsetSum: sum, at: at}
	}
	s.prev = next
}

// ---- public snapshot access ------------------------------------------

// ClusterMetricsSnapshot returns the most recent metrics snapshot for a
// configured cluster, or (ClusterMetrics{}, false) when none is available
// yet (collector not started, first refresh not finished, or cluster not
// configured — e.g. ad-hoc private clusters).
func (r *Registry) ClusterMetricsSnapshot(cluster string) (ClusterMetrics, bool) {
	mc := r.metricsCollector()
	if mc == nil {
		return ClusterMetrics{}, false
	}
	mc.statesMu.RLock()
	state, ok := mc.states[cluster]
	mc.statesMu.RUnlock()
	if !ok {
		return ClusterMetrics{}, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if !state.hasSnap {
		return ClusterMetrics{}, false
	}
	return state.snapshot, true
}

func (r *Registry) metricsCollector() *metricsCollector {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.metrics
}

// applyTopicMetrics enriches a slice of TopicInfo with the latest cached
// metrics for its cluster, if any. Unknown fields stay nil so callers can
// distinguish "not yet measured" from "known zero".
func (r *Registry) applyTopicMetrics(cluster string, topics []TopicInfo) {
	snap, ok := r.ClusterMetricsSnapshot(cluster)
	if !ok {
		return
	}
	for i := range topics {
		m, ok := snap.PerTopic[topics[i].Name]
		if !ok {
			continue
		}
		topics[i].Messages = ptrInt64(m.Messages)
		if m.HaveSize {
			topics[i].SizeBytes = ptrInt64(m.SizeBytes)
		}
		if m.HaveRetention {
			topics[i].RetentionMs = ptrInt64(m.RetentionMs)
		}
		if m.HaveRate {
			topics[i].RatePerSec = ptrFloat64(m.RatePerSec)
		}
		if m.HaveLag {
			topics[i].Lag = ptrInt64(m.Lag)
		}
	}
}

// applyClusterAggregates copies per-cluster totals from the collector onto
// the ClusterInfo. No-op when no snapshot is available.
func (r *Registry) applyClusterAggregates(info *ClusterInfo) {
	snap, ok := r.ClusterMetricsSnapshot(info.Name)
	if !ok {
		return
	}
	b := snap.Brokers
	t := snap.Topics
	g := snap.Groups
	tm := snap.TotalMessages
	info.Brokers = &b
	info.Topics = &t
	info.Groups = &g
	info.TotalMessages = &tm
	if snap.HaveLag {
		v := snap.TotalLag
		info.TotalLag = &v
	}
	if snap.HaveRate {
		v := snap.TotalRate
		info.TotalRatePerSec = &v
	}
}

func ptrInt64(v int64) *int64       { return &v }
func ptrFloat64(v float64) *float64 { return &v }
