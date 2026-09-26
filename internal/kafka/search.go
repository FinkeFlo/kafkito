// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// ParseErrorOffset records the location and reason of a single message that
// could not be parsed during a structured search (JSONPath, XPath, …).
type ParseErrorOffset struct {
	Partition int32  `json:"partition"`
	Offset    int64  `json:"offset"`
	Error     string `json:"error"`
}

// parseErrorOffsetsCap is the maximum number of parse error locations kept per
// search response. It prevents unbounded memory growth on topics with many
// malformed messages.
const parseErrorOffsetsCap = 50

// SearchStats is the per-response summary.
type SearchStats struct {
	Scanned         int  `json:"scanned"`
	Matched         int  `json:"matched"`
	BudgetExhausted bool `json:"budget_exhausted"`
	// TimedOut is true when the scan stopped because the per-call timeout
	// elapsed before the budget or the partition range was exhausted.
	TimedOut bool `json:"timed_out"`
	// MoreAvailable is true when the resolved range was not fully scanned, i.e.
	// a continuation call with NextCursors would scan additional records.
	MoreAvailable bool                     `json:"more_available"`
	Direction     SearchDirection          `json:"direction"`
	NextCursors   map[int32]int64          `json:"next_cursors,omitempty"`
	ResolvedRange map[int32]PartitionRange `json:"resolved_range,omitempty"`
	ParseErrors   int                      `json:"parse_errors"`
	// ParseErrorOffsets lists up to 50 partition/offset pairs where a message
	// could not be parsed. Use this to locate and inspect the raw messages.
	ParseErrorOffsets []ParseErrorOffset `json:"parse_error_offsets,omitempty"`
	Durations         map[string]int64   `json:"durations_ms,omitempty"`
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
		// o.Path is the caller-authored XPath query itself (like a grep
		// pattern), not user data spliced into a privileged expression -
		// there is no base query for it to escape, and the antchfx/xpath
		// engine used here has no variable-binding API to parameterize
		// against anyway. Length/complexity is bounded by the caller
		// (see maxSearchPathLen in internal/server) to guard against
		// pathological expressions.
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

// resolveSearchRange picks the [start, end) offset range to scan per
// partition from the partition offsets, the time bounds and the
// continuation cursors, which override the end (newest-first) or the start
// (oldest-first).
func resolveSearchRange(opts SearchOptions, offs *topicOffsets) map[int32]PartitionRange {
	out := make(map[int32]PartitionRange, len(offs.parts))
	for _, p := range offs.parts {
		begin, finish := offs.bounds(p)
		if c, ok := opts.Cursors[p]; ok {
			if opts.Direction == DirNewestFirst {
				finish = min(finish, c)
			} else {
				begin = max(begin, c)
			}
		}
		if finish > begin {
			out[p] = PartitionRange{Start: begin, End: finish}
		}
	}
	return out
}

// Search defaults and caps.
const (
	defaultSearchBudget = 10000
	maxSearchBudget     = 500000
	// searchChunkSize is how many offsets per partition a newest-first
	// search reads per backward step. Larger values reduce re-seek overhead;
	// smaller values give tighter stop-on-limit responsiveness when matches
	// are dense near the end.
	searchChunkSize int64 = 4000
)

// SearchMessages scans up to opts.Budget records across the selected partitions
// and returns those matching the compiled predicate. It is a read-only op; it
// never commits offsets.
func (r *Registry) SearchMessages(ctx context.Context, cluster, topic string, opts SearchOptions) (*SearchResult, error) {
	cfg, ok := r.ConfigFor(cluster)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, cluster)
	}
	opts = opts.withDefaults()
	mt, err := opts.compile()
	if err != nil {
		return nil, err
	}

	// Partitions are not validated: an unknown partition resolves to an
	// empty range and therefore to an empty result.
	offs, err := r.readerOffsets(ctx, cluster, topic, offsetsQuery{
		partition: opts.Partition, unchecked: true, fromTSMs: opts.FromTS, toTSMs: opts.ToTS,
	})
	if err != nil {
		return nil, err
	}
	ranges := resolveSearchRange(opts, offs)
	if len(ranges) == 0 {
		return &SearchResult{
			Messages: []Message{},
			Stats: SearchStats{
				Direction:     opts.Direction,
				ResolvedRange: map[int32]PartitionRange{},
			},
		}, nil
	}

	started := time.Now()
	sc := &searchScan{
		match:   mt,
		dec:     r.recordDecoder(cluster, topic),
		lowest:  make(map[int32]int64, len(ranges)),
		highest: make(map[int32]int64, len(ranges)),
	}
	scan := recordScan{cluster: cluster, topic: topic, role: "search", cfg: cfg, ranges: ranges, drainAfter: 1}
	if opts.Direction == DirNewestFirst {
		scan.chunk = searchChunkSize
	}
	budgetExhausted, timedOut, err := r.runSearch(ctx, scan, opts, sc)
	if err != nil {
		return nil, err
	}

	matches := sc.matches
	sortMessages(matches, opts.Direction == DirNewestFirst)
	matched := len(matches)
	if matched > opts.Limit {
		matches = matches[:opts.Limit]
	}
	nextCursors, moreAvailable := sc.continuation(ranges, opts.Direction)

	return &SearchResult{Messages: matches, Stats: SearchStats{
		Scanned:           sc.scanned,
		Matched:           matched,
		BudgetExhausted:   budgetExhausted,
		TimedOut:          timedOut,
		MoreAvailable:     moreAvailable,
		Direction:         opts.Direction,
		NextCursors:       nextCursors,
		ResolvedRange:     ranges,
		ParseErrors:       sc.parseErrors,
		ParseErrorOffsets: sc.parseErrorOffsets,
		Durations:         map[string]int64{"total": time.Since(started).Milliseconds()},
	}}, nil
}

func (o SearchOptions) withDefaults() SearchOptions {
	if o.Limit <= 0 {
		o.Limit = defaultConsumeLimit
	}
	o.Limit = min(o.Limit, maxConsumeLimit)
	if o.Budget <= 0 {
		o.Budget = defaultSearchBudget
	}
	o.Budget = min(o.Budget, maxSearchBudget)
	if o.Direction == "" {
		o.Direction = DirNewestFirst
	}
	if o.Timeout <= 0 {
		o.Timeout = 8 * time.Second
	}
	return o
}

// runSearch feeds the scanned records into sc until the ranges are drained,
// the budget is spent, enough matches were found (StopOnLimit) or
// opts.Timeout elapses. Budget and limit are checked after every poll.
func (r *Registry) runSearch(ctx context.Context, s recordScan, opts SearchOptions, sc *searchScan) (budgetExhausted, timedOut bool, err error) {
	pollCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	for batch, err := range r.scanRecords(pollCtx, s) {
		if errors.Is(err, context.DeadlineExceeded) {
			return false, true, nil
		}
		if err != nil {
			return false, false, err
		}
		for _, rec := range batch {
			sc.visit(ctx, rec)
		}
		if sc.scanned >= opts.Budget {
			return true, false, nil
		}
		if opts.StopOnLimit && len(sc.matches) >= opts.Limit {
			break
		}
	}
	return false, false, nil
}

// searchScan accumulates the matches and stats of one SearchMessages call.
type searchScan struct {
	match             matcher
	dec               recordDecoder
	matches           []Message
	scanned           int
	parseErrors       int
	parseErrorOffsets []ParseErrorOffset
	// lowest/highest offset processed per partition, for the next cursors.
	lowest, highest map[int32]int64
}

func (sc *searchScan) visit(ctx context.Context, rec *kgo.Record) {
	p := rec.Partition
	if cur, ok := sc.highest[p]; !ok || rec.Offset > cur {
		sc.highest[p] = rec.Offset
	}
	if cur, ok := sc.lowest[p]; !ok || rec.Offset < cur {
		sc.lowest[p] = rec.Offset
	}
	sc.scanned++
	// Match against the full, untruncated record: truncating first would
	// hide contains-matches past maxMessageValueBytes and corrupt
	// JSONPath/XPath/JS parsing of any larger record. Where a masking rule
	// applies, the value is matched in its masked form, so masked content
	// cannot be found by searching for it. A hit is rebuilt through the
	// truncating, masking path so the response carries the same bounded
	// preview as every other consume path.
	full := sc.dec.matchMessage(ctx, rec)
	hit, err := sc.match.match(&full)
	if err != nil {
		sc.parseError(ctx, rec, err)
		return
	}
	if hit {
		sc.matches = append(sc.matches, sc.dec.message(ctx, rec))
	}
}

// parseErrorWithheld replaces the parse error text on topics with an active
// masking rule: parser and JS errors can quote parts of the value.
const parseErrorWithheld = "value could not be evaluated (details withheld: data masking applies to this topic)"

func (sc *searchScan) parseError(ctx context.Context, rec *kgo.Record, err error) {
	sc.parseErrors++
	msg := err.Error()
	if sc.dec.masks {
		msg = parseErrorWithheld
	}
	slog.WarnContext(ctx, "search: skipping message – parse error",
		"partition", rec.Partition,
		"offset", rec.Offset,
		"error", msg)
	if len(sc.parseErrorOffsets) < parseErrorOffsetsCap {
		sc.parseErrorOffsets = append(sc.parseErrorOffsets, ParseErrorOffset{
			Partition: rec.Partition,
			Offset:    rec.Offset,
			Error:     msg,
		})
	}
}

// continuation returns the per-partition cursors for a follow-up search and
// whether that search would scan anything. Partitions without a scanned
// record are treated as fully scanned.
func (sc *searchScan) continuation(ranges map[int32]PartitionRange, dir SearchDirection) (map[int32]int64, bool) {
	next := make(map[int32]int64, len(ranges))
	more := false
	for p, rng := range ranges {
		if dir == DirNewestFirst {
			// A follow-up scans [Start, lowest): everything above is done.
			next[p] = rng.Start
			if lo, ok := sc.lowest[p]; ok {
				next[p] = lo
			}
			more = more || next[p] > rng.Start
			continue
		}
		next[p] = rng.End
		if hi, ok := sc.highest[p]; ok {
			next[p] = hi + 1
		}
		more = more || next[p] < rng.End
	}
	return next, more
}
