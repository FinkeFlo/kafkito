// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antchfx/xpath"
	"github.com/ohler55/ojg/jp"
	"github.com/twmb/franz-go/pkg/kgo"
)

// SearchMode selects the predicate family used while scanning.
type SearchMode string

// Supported search modes.
const (
	SearchModeContains SearchMode = "contains"
	SearchModeJSONPath SearchMode = "jsonpath"
	SearchModeXPath    SearchMode = "xpath"
	SearchModeJS       SearchMode = "js"
)

// SearchOp enumerates the comparison operators for path modes.
type SearchOp string

// Supported search operators.
const (
	OpExists   SearchOp = "exists"
	OpEq       SearchOp = "eq"
	OpNeq      SearchOp = "ne"
	OpContains SearchOp = "contains"
	OpRegex    SearchOp = "regex"
	OpGt       SearchOp = "gt"
	OpLt       SearchOp = "lt"
	OpGte      SearchOp = "gte"
	OpLte      SearchOp = "lte"
)

// SearchZone names the record part to consider for contains-mode matching.
// Path modes always operate on the record value.
type SearchZone string

// Search zones.
const (
	ZoneValue   SearchZone = "value"
	ZoneKey     SearchZone = "key"
	ZoneHeaders SearchZone = "headers"
)

// SearchDirection chooses the time ordering for results.
type SearchDirection string

// Search directions.
const (
	DirNewestFirst SearchDirection = "newest_first"
	DirOldestFirst SearchDirection = "oldest_first"
)

// SearchOptions configures SearchMessages.
type SearchOptions struct {
	Partition int32
	Limit     int
	Budget    int
	Direction SearchDirection

	Mode  SearchMode
	Path  string
	Op    SearchOp
	Value string
	Zones []SearchZone

	// FromTS/ToTS are UNIX millis. Zero means "not set"; zero lower bound defaults
	// to the partition start, zero upper bound to the partition end.
	FromTS int64
	ToTS   int64

	// Cursors (per-partition) for continuation. If set they override the begin
	// offsets resolved from FromTS/ToTS.
	Cursors map[int32]int64

	// StopOnLimit returns once Limit matches have been collected; otherwise we
	// scan up to Budget records and return all matches found.
	StopOnLimit bool

	Timeout time.Duration
}

// SearchStats is the per-response summary.
type SearchStats struct {
	Scanned         int                      `json:"scanned"`
	Matched         int                      `json:"matched"`
	BudgetExhausted bool                     `json:"budget_exhausted"`
	Direction       SearchDirection          `json:"direction"`
	NextCursors     map[int32]int64          `json:"next_cursors,omitempty"`
	ResolvedRange   map[int32]PartitionRange `json:"resolved_range,omitempty"`
	ParseErrors     int                      `json:"parse_errors"`
	Durations       map[string]int64         `json:"durations_ms,omitempty"`
}

// PartitionRange reports the offset window actually scanned on a partition.
type PartitionRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// SearchResult is the value returned by SearchMessages.
type SearchResult struct {
	Messages []Message   `json:"messages"`
	Stats    SearchStats `json:"stats"`
}

// compile turns the options into a matcher. Nil matcher means "pass all".
func (o SearchOptions) compile() (matcher, error) {
	if strings.TrimSpace(o.Value) == "" && o.Op != OpExists {
		// Empty needle with no predicate = no filter.
		return passAllMatcher{}, nil
	}
	zones := o.Zones
	if len(zones) == 0 {
		zones = []SearchZone{ZoneValue}
	}
	switch o.Mode {
	case "", SearchModeContains:
		return &containsMatcher{needle: o.Value, zones: zones}, nil
	case SearchModeJSONPath:
		expr, err := jp.ParseString(o.Path)
		if err != nil {
			return nil, fmt.Errorf("jsonpath %q: %w", o.Path, err)
		}
		return newPathMatcher(jsonPathEval(expr), o.Op, o.Value)
	case SearchModeXPath:
		expr, err := xpath.Compile(o.Path)
		if err != nil {
			return nil, fmt.Errorf("xpath %q: %w", o.Path, err)
		}
		return newPathMatcher(xmlPathEval(expr), o.Op, o.Value)
	case SearchModeJS:
		return newJSMatcher(o.Value)
	default:
		return nil, fmt.Errorf("unknown search mode: %s", o.Mode)
	}
}

// requested time range, cursor overrides and partition metadata.
func resolveSearchRange(opts SearchOptions, partitions []int32, starts, ends map[int32]int64, fromOffsets, toOffsets map[int32]int64) map[int32]PartitionRange {
	out := make(map[int32]PartitionRange, len(partitions))
	for _, p := range partitions {
		start := starts[p]
		end := ends[p]
		if end <= start {
			continue
		}
		begin := start
		finish := end
		if opts.FromTS > 0 {
			if o, ok := fromOffsets[p]; ok && o > begin {
				begin = o
			}
		}
		if opts.ToTS > 0 {
			if o, ok := toOffsets[p]; ok && o < finish {
				finish = o
			}
		}
		// Cursors override begin/end based on direction.
		if c, ok := opts.Cursors[p]; ok {
			switch opts.Direction {
			case DirNewestFirst:
				if c < finish {
					finish = c
				}
			default:
				if c > begin {
					begin = c
				}
			}
		}
		if finish <= begin {
			continue
		}
		out[p] = PartitionRange{Start: begin, End: finish}
	}
	return out
}

// SearchMessages scans up to opts.Budget records across the selected partitions
// and returns those matching the compiled predicate. It is a read-only op; it
// never commits offsets.
func (r *Registry) SearchMessages(ctx context.Context, cluster, topic string, opts SearchOptions) (*SearchResult, error) {
	cfg, ok := r.clusters[cluster]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit > maxConsumeLimit {
		opts.Limit = maxConsumeLimit
	}
	if opts.Budget <= 0 {
		opts.Budget = 10000
	}
	if opts.Budget > 500000 {
		opts.Budget = 500000
	}
	if opts.Direction == "" {
		opts.Direction = DirNewestFirst
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 8 * time.Second
	}

	mt, err := opts.compile()
	if err != nil {
		return nil, err
	}
	policy := r.MaskingPolicy(cluster)
	dec := r.srDecoderFor(cluster)

	adm, err := r.Admin(cluster)
	if err != nil {
		return nil, err
	}
	admCtx, admCancel := context.WithTimeout(ctx, 3*time.Second)
	defer admCancel()

	md, err := adm.Metadata(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata for topic %q on cluster %q: %w", topic, cluster, err)
	}
	t, ok := md.Topics[topic]
	if !ok || t.Err != nil {
		return nil, fmt.Errorf("topic %q not found on cluster %q", topic, cluster)
	}
	parts := make([]int32, 0, len(t.Partitions))
	if opts.Partition >= 0 {
		parts = append(parts, opts.Partition)
	} else {
		for _, p := range t.Partitions {
			parts = append(parts, p.Partition)
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })

	starts, err := adm.ListStartOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list start offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	ends, err := adm.ListEndOffsets(admCtx, topic)
	if err != nil {
		return nil, fmt.Errorf("list end offsets for topic %q on cluster %q: %w", topic, cluster, err)
	}
	startMap := make(map[int32]int64, len(parts))
	endMap := make(map[int32]int64, len(parts))
	for _, p := range parts {
		if so, ok := starts.Lookup(topic, p); ok {
			startMap[p] = so.Offset
		}
		if eo, ok := ends.Lookup(topic, p); ok {
			endMap[p] = eo.Offset
		}
	}

	lookupTS := func(ms int64) (map[int32]int64, error) {
		lo, err := adm.ListOffsetsAfterMilli(admCtx, ms, topic)
		if err != nil {
			return nil, err
		}
		m := make(map[int32]int64, len(parts))
		for _, p := range parts {
			if po, ok := lo.Lookup(topic, p); ok {
				m[p] = po.Offset
			}
		}
		return m, nil
	}
	var fromOffsets, toOffsets map[int32]int64
	if opts.FromTS > 0 {
		fromOffsets, err = lookupTS(opts.FromTS)
		if err != nil {
			return nil, fmt.Errorf("list offsets after ts=%d for topic %q on cluster %q: %w", opts.FromTS, topic, cluster, err)
		}
	}
	if opts.ToTS > 0 {
		toOffsets, err = lookupTS(opts.ToTS)
		if err != nil {
			return nil, fmt.Errorf("list offsets after ts=%d for topic %q on cluster %q: %w", opts.ToTS, topic, cluster, err)
		}
	}

	ranges := resolveSearchRange(opts, parts, startMap, endMap, fromOffsets, toOffsets)
	if len(ranges) == 0 {
		return &SearchResult{
			Messages: []Message{},
			Stats: SearchStats{
				Direction:     opts.Direction,
				ResolvedRange: map[int32]PartitionRange{},
			},
		}, nil
	}

	consumeOpts := clientOpts(cfg, r.log.With("cluster", cluster, "role", "search"))
	// Initial partition assignment depends on direction: oldest-first scans
	// forward from rng.Start in a single pass; newest-first consumes backward
	// in chunks by re-seeking between iterations.
	initialOffsets := make(map[int32]kgo.Offset, len(ranges))
	// upperBounds[p] is the exclusive upper offset still to process. For
	// newest-first it shrinks each chunk; for oldest-first it stays at rng.End.
	upperBounds := make(map[int32]int64, len(ranges))
	// lowerBounds[p] is the inclusive lower offset we are allowed to read.
	lowerBounds := make(map[int32]int64, len(ranges))
	// chunkStarts[p] is the offset the current consumer is positioned at.
	chunkStarts := make(map[int32]int64, len(ranges))

	// chunkSize controls how many offsets per partition we consume per backward
	// iteration. Larger values reduce re-seek overhead; smaller values give
	// tighter stop-on-limit responsiveness when matches are dense near the end.
	const chunkSize int64 = 4000

	for p, rng := range ranges {
		upperBounds[p] = rng.End
		lowerBounds[p] = rng.Start
		switch opts.Direction {
		case DirNewestFirst:
			cs := rng.End - chunkSize
			if cs < rng.Start {
				cs = rng.Start
			}
			chunkStarts[p] = cs
		default:
			chunkStarts[p] = rng.Start
		}
		initialOffsets[p] = kgo.NewOffset().At(chunkStarts[p])
	}
	consumeOpts = append(consumeOpts,
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{topic: initialOffsets}),
		kgo.FetchMaxWait(500*time.Millisecond),
	)
	cl, err := kgo.NewClient(consumeOpts...)
	if err != nil {
		return nil, fmt.Errorf("create search client for topic %q on cluster %q: %w", topic, cluster, err)
	}
	defer cl.Close()

	pollCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	var matches []Message
	scanned := 0
	parseErrors := 0
	// highestOffset tracks the highest offset we've processed per partition
	// (used for oldest-first next_cursor).
	highestOffset := make(map[int32]int64, len(ranges))
	// lowestOffset tracks the lowest offset we've processed per partition
	// (used for newest-first next_cursor).
	lowestOffset := make(map[int32]int64, len(ranges))
	// chunkDone[p] flips true within a poll once we observe an offset ≥
	// upperBounds[p], signalling the current backward chunk is exhausted and
	// the partition should be reseeked.
	chunkDone := make(map[int32]bool, len(ranges))
	budgetExhausted := false
	started := time.Now()

	// advanceNewestChunks reseeks any partitions whose current chunk is done
	// to the next backward window. Returns false when every partition has
	// reached its lowerBounds and no further scan is possible.
	advanceNewestChunks := func() bool {
		removeList := make([]int32, 0, len(upperBounds))
		addOffsets := make(map[int32]kgo.Offset, len(upperBounds))
		for p := range upperBounds {
			if !chunkDone[p] {
				continue
			}
			// Current chunk has been fully processed: move window down.
			upperBounds[p] = chunkStarts[p]
			chunkDone[p] = false
			if upperBounds[p] <= lowerBounds[p] {
				delete(upperBounds, p)
				removeList = append(removeList, p)
				continue
			}
			cs := upperBounds[p] - chunkSize
			if cs < lowerBounds[p] {
				cs = lowerBounds[p]
			}
			chunkStarts[p] = cs
			removeList = append(removeList, p)
			addOffsets[p] = kgo.NewOffset().At(cs)
		}
		if len(removeList) > 0 {
			cl.RemoveConsumePartitions(map[string][]int32{topic: removeList})
		}
		if len(addOffsets) > 0 {
			cl.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: addOffsets})
		}
		return len(upperBounds) > 0
	}

scan:
	for scanned < opts.Budget {
		fetches := cl.PollFetches(pollCtx)
		if err := pollCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, err
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				if errors.Is(e.Err, context.Canceled) || errors.Is(e.Err, context.DeadlineExceeded) {
					continue
				}
				return nil, fmt.Errorf("fetch p%d: %w", e.Partition, e.Err)
			}
		}
		batchEmpty := fetches.Empty()
		fetches.EachRecord(func(rec *kgo.Record) {
			p := rec.Partition
			ub, active := upperBounds[p]
			if !active {
				return
			}
			if rec.Offset >= ub {
				// Past the current chunk's upper bound — either already
				// processed in a previous chunk (newest-first) or past end
				// (oldest-first). Mark chunk complete so we reseek.
				chunkDone[p] = true
				return
			}
			if rec.Offset < lowerBounds[p] {
				return
			}
			if cur, ok := highestOffset[p]; !ok || rec.Offset > cur {
				highestOffset[p] = rec.Offset
			}
			if cur, ok := lowestOffset[p]; !ok || rec.Offset < cur {
				lowestOffset[p] = rec.Offset
			}
			scanned++
			msg := recordToMessage(rec)
			msg.applySRDecoder(ctx, dec, rec.Key, rec.Value)
			hit, err := mt.match(&msg)
			if err != nil {
				parseErrors++
				return
			}
			if hit {
				if !policy.IsEmpty() && msg.Value != "" {
					if mv, did := policy.Apply(topic, msg.Value); did {
						msg.Value = mv
						msg.Masked = true
					}
				}
				matches = append(matches, msg)
			}
			// In newest-first mode, the chunk is complete once we've observed
			// the record at upperBounds-1.
			if opts.Direction == DirNewestFirst && rec.Offset == ub-1 {
				chunkDone[p] = true
			}
			// In oldest-first mode the chunk extends to rng.End; completion
			// happens when rec.Offset == ub-1.
			if opts.Direction == DirOldestFirst && rec.Offset == ub-1 {
				chunkDone[p] = true
			}
		})
		if scanned >= opts.Budget {
			budgetExhausted = true
			break scan
		}
		if opts.StopOnLimit && len(matches) >= opts.Limit {
			break scan
		}
		switch opts.Direction {
		case DirNewestFirst:
			anyDone := false
			for p := range chunkDone {
				if chunkDone[p] {
					anyDone = true
					break
				}
			}
			if batchEmpty {
				// Drained without hitting upper bound (gaps / compaction /
				// broker idle): treat every active partition as chunk-done so
				// we advance the window.
				for p := range upperBounds {
					chunkDone[p] = true
				}
				anyDone = true
			}
			if anyDone {
				if !advanceNewestChunks() {
					break scan
				}
			}
		case DirOldestFirst:
			// Single forward pass; empty fetch or all partitions done = stop.
			if batchEmpty {
				break scan
			}
			allDone := len(upperBounds) > 0
			for p := range upperBounds {
				if !chunkDone[p] {
					allDone = false
					break
				}
			}
			if allDone {
				break scan
			}
		}
	}

	// Sort and trim.
	if opts.Direction == DirNewestFirst {
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].Timestamp != matches[j].Timestamp {
				return matches[i].Timestamp > matches[j].Timestamp
			}
			if matches[i].Partition != matches[j].Partition {
				return matches[i].Partition < matches[j].Partition
			}
			return matches[i].Offset > matches[j].Offset
		})
	} else {
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].Timestamp != matches[j].Timestamp {
				return matches[i].Timestamp < matches[j].Timestamp
			}
			if matches[i].Partition != matches[j].Partition {
				return matches[i].Partition < matches[j].Partition
			}
			return matches[i].Offset < matches[j].Offset
		})
	}
	matched := len(matches)
	if matched > opts.Limit {
		matches = matches[:opts.Limit]
	}

	nextCursors := make(map[int32]int64, len(ranges))
	for p, rng := range ranges {
		switch opts.Direction {
		case DirNewestFirst:
			if lo, ok := lowestOffset[p]; ok {
				// Future call should scan [Start, lo): we've processed lo+.
				nextCursors[p] = lo
			} else {
				nextCursors[p] = rng.Start
			}
		default:
			if hi, ok := highestOffset[p]; ok {
				nextCursors[p] = hi + 1
			} else {
				nextCursors[p] = rng.End
			}
		}
	}

	stats := SearchStats{
		Scanned:         scanned,
		Matched:         matched,
		BudgetExhausted: budgetExhausted,
		Direction:       opts.Direction,
		NextCursors:     nextCursors,
		ResolvedRange:   ranges,
		ParseErrors:     parseErrors,
		Durations:       map[string]int64{"total": time.Since(started).Milliseconds()},
	}
	return &SearchResult{Messages: matches, Stats: stats}, nil
}
